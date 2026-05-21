use std::fs;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use chrono::Utc;
use rusqlite::{params, Connection, OptionalExtension};
use serde::Serialize;
use serde_json::Value;

#[derive(Debug, Clone, Serialize)]
pub struct RoomRecord {
    pub room_id: String,
    pub name: Option<String>,
    pub canonical_alias: Option<String>,
    pub avatar_mxc: Option<String>,
    pub joined_at: Option<i64>,
    pub last_prev_batch: Option<String>,
    pub backfill_token: Option<String>,
    pub backfill_done: bool,
}

#[derive(Debug, Clone, Serialize)]
pub struct EventRecord {
    pub event_id: String,
    pub room_id: String,
    pub origin_server_ts: Option<i64>,
    pub sender: Option<String>,
    pub event_type: String,
    pub state_key: Option<String>,
    pub msgtype: Option<String>,
    pub relates_to_event_id: Option<String>,
    pub relation_type: Option<String>,
    pub redacts_event_id: Option<String>,
    pub is_encrypted: bool,
    pub is_redacted: bool,
    pub body_text: Option<String>,
    pub formatted_body_html: Option<String>,
    pub raw_event: Value,
    pub decrypted_event: Option<Value>,
    pub canonical_sha256: String,
    pub received_at: i64,
    pub source_batch: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct MediaRefRecord {
    pub event_id: String,
    pub field_path: String,
    pub mxc_uri: String,
    pub object_hash: Option<String>,
    pub encrypted_file_json: Option<Value>,
}

#[derive(Debug, Clone, Serialize)]
pub struct GapRecord {
    pub room_id: Option<String>,
    pub event_id: Option<String>,
    pub kind: String,
    pub detail: String,
}

pub struct ArchiveStore {
    db_path: PathBuf,
    conn: Connection,
}

impl ArchiveStore {
    pub fn open(archive_dir: &Path) -> Result<Self> {
        fs::create_dir_all(archive_dir)
            .with_context(|| format!("create archive directory {}", archive_dir.display()))?;
        let db_path = archive_dir.join("archive.sqlite");
        let conn = Connection::open(&db_path)
            .with_context(|| format!("open archive database {}", db_path.display()))?;
        conn.pragma_update(None, "journal_mode", "WAL")?;
        conn.pragma_update(None, "foreign_keys", "ON")?;
        conn.pragma_update(None, "synchronous", "NORMAL")?;
        let store = Self { db_path, conn };
        store.migrate()?;
        Ok(store)
    }

    pub fn db_path(&self) -> &Path {
        &self.db_path
    }

    fn migrate(&self) -> Result<()> {
        self.conn.execute_batch(
            r#"
            CREATE TABLE IF NOT EXISTS rooms (
                room_id TEXT PRIMARY KEY,
                canonical_alias TEXT,
                name TEXT,
                avatar_mxc TEXT,
                joined_at INTEGER,
                left_at INTEGER,
                last_prev_batch TEXT,
                backfill_token TEXT,
                backfill_done INTEGER NOT NULL DEFAULT 0
            );
            CREATE TABLE IF NOT EXISTS events (
                event_id TEXT PRIMARY KEY,
                room_id TEXT NOT NULL,
                origin_server_ts INTEGER,
                sender TEXT,
                type TEXT NOT NULL,
                state_key TEXT,
                msgtype TEXT,
                relates_to_event_id TEXT,
                relation_type TEXT,
                redacts_event_id TEXT,
                is_encrypted INTEGER NOT NULL DEFAULT 0,
                is_redacted INTEGER NOT NULL DEFAULT 0,
                body_text TEXT,
                formatted_body_html TEXT,
                raw_event_zstd BLOB NOT NULL,
                decrypted_event_zstd BLOB,
                canonical_sha256 TEXT NOT NULL,
                received_at INTEGER NOT NULL,
                source_batch TEXT NOT NULL
            );
            CREATE INDEX IF NOT EXISTS events_room_ts_idx ON events(room_id, origin_server_ts);
            CREATE TABLE IF NOT EXISTS state_events (
                room_id TEXT NOT NULL,
                type TEXT NOT NULL,
                state_key TEXT NOT NULL,
                event_id TEXT NOT NULL,
                effective_ts INTEGER,
                PRIMARY KEY (room_id, type, state_key, event_id)
            );
            CREATE TABLE IF NOT EXISTS media_refs (
                event_id TEXT NOT NULL,
                field_path TEXT NOT NULL,
                mxc_uri TEXT NOT NULL,
                object_hash TEXT,
                encrypted_file_json_zstd BLOB,
                PRIMARY KEY (event_id, field_path, mxc_uri)
            );
            CREATE INDEX IF NOT EXISTS media_refs_mxc_idx ON media_refs(mxc_uri);
            CREATE TABLE IF NOT EXISTS media_objects (
                object_hash TEXT PRIMARY KEY,
                algo TEXT NOT NULL,
                size INTEGER NOT NULL,
                mimetype TEXT,
                original_filename TEXT,
                storage_path TEXT NOT NULL,
                ciphertext_sha256 TEXT,
                plaintext_sha256 TEXT,
                created_at INTEGER NOT NULL
            );
            CREATE TABLE IF NOT EXISTS sync_state (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS gaps (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                room_id TEXT,
                event_id TEXT,
                kind TEXT NOT NULL,
                detail TEXT NOT NULL,
                created_at INTEGER NOT NULL
            );
            CREATE VIRTUAL TABLE IF NOT EXISTS event_fts USING fts5(
                event_id UNINDEXED,
                room_id UNINDEXED,
                body_text,
                formatted_body_html,
                tokenize='unicode61'
            );
            "#,
        )?;
        Ok(())
    }

    pub fn get_state(&self, key: &str) -> Result<Option<String>> {
        self.conn
            .query_row("SELECT value FROM sync_state WHERE key=?", [key], |row| {
                row.get(0)
            })
            .optional()
            .map_err(Into::into)
    }

    pub fn set_state(&self, key: &str, value: &str) -> Result<()> {
        self.conn.execute(
            "INSERT INTO sync_state(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            params![key, value],
        )?;
        Ok(())
    }

    pub fn upsert_room(&self, room: &RoomRecord) -> Result<()> {
        self.conn.execute(
            r#"
            INSERT INTO rooms(room_id, canonical_alias, name, avatar_mxc, joined_at, last_prev_batch, backfill_token, backfill_done)
            VALUES(?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(room_id) DO UPDATE SET
                canonical_alias=COALESCE(excluded.canonical_alias, rooms.canonical_alias),
                name=COALESCE(excluded.name, rooms.name),
                avatar_mxc=COALESCE(excluded.avatar_mxc, rooms.avatar_mxc),
                joined_at=COALESCE(rooms.joined_at, excluded.joined_at),
                last_prev_batch=COALESCE(excluded.last_prev_batch, rooms.last_prev_batch),
                backfill_token=COALESCE(excluded.backfill_token, rooms.backfill_token),
                backfill_done=CASE WHEN excluded.backfill_done=1 THEN 1 ELSE rooms.backfill_done END
            "#,
            params![
                room.room_id,
                room.canonical_alias,
                room.name,
                room.avatar_mxc,
                room.joined_at,
                room.last_prev_batch,
                room.backfill_token,
                i64::from(room.backfill_done),
            ],
        )?;
        Ok(())
    }

    pub fn rooms_for_backfill(&self, limit: usize) -> Result<Vec<RoomRecord>> {
        let mut stmt = self.conn.prepare(
            r#"
            SELECT room_id, name, canonical_alias, avatar_mxc, joined_at, last_prev_batch, backfill_token, backfill_done
            FROM rooms
            WHERE backfill_done=0 AND COALESCE(backfill_token, last_prev_batch) IS NOT NULL
            ORDER BY room_id
            LIMIT ?
            "#,
        )?;
        let rows = stmt.query_map([limit as i64], read_room)?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn list_rooms(&self) -> Result<Vec<RoomRecord>> {
        let mut stmt = self.conn.prepare(
            r#"
            SELECT room_id, name, canonical_alias, avatar_mxc, joined_at, last_prev_batch, backfill_token, backfill_done
            FROM rooms
            ORDER BY COALESCE(name, canonical_alias, room_id)
            "#,
        )?;
        let rows = stmt.query_map([], read_room)?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn update_backfill_cursor(
        &self,
        room_id: &str,
        token: Option<&str>,
        done: bool,
    ) -> Result<()> {
        self.conn.execute(
            "UPDATE rooms SET backfill_token=?, backfill_done=? WHERE room_id=?",
            params![token, i64::from(done), room_id],
        )?;
        Ok(())
    }

    pub fn insert_event(&self, event: &EventRecord) -> Result<()> {
        let raw = compress_json(&event.raw_event)?;
        let decrypted = match &event.decrypted_event {
            Some(value) => Some(compress_json(value)?),
            None => None,
        };
        self.conn.execute(
            r#"
            INSERT INTO events(
                event_id, room_id, origin_server_ts, sender, type, state_key, msgtype,
                relates_to_event_id, relation_type, redacts_event_id, is_encrypted, is_redacted,
                body_text, formatted_body_html, raw_event_zstd, decrypted_event_zstd,
                canonical_sha256, received_at, source_batch
            ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(event_id) DO UPDATE SET
                is_redacted=excluded.is_redacted,
                redacts_event_id=COALESCE(excluded.redacts_event_id, events.redacts_event_id),
                body_text=COALESCE(excluded.body_text, events.body_text),
                formatted_body_html=COALESCE(excluded.formatted_body_html, events.formatted_body_html)
            "#,
            params![
                event.event_id,
                event.room_id,
                event.origin_server_ts,
                event.sender,
                event.event_type,
                event.state_key,
                event.msgtype,
                event.relates_to_event_id,
                event.relation_type,
                event.redacts_event_id,
                i64::from(event.is_encrypted),
                i64::from(event.is_redacted),
                event.body_text,
                event.formatted_body_html,
                raw,
                decrypted,
                event.canonical_sha256,
                event.received_at,
                event.source_batch,
            ],
        )?;
        self.conn.execute(
            "DELETE FROM event_fts WHERE event_id=?",
            [event.event_id.as_str()],
        )?;
        if event.body_text.is_some() || event.formatted_body_html.is_some() {
            self.conn.execute(
                "INSERT INTO event_fts(event_id, room_id, body_text, formatted_body_html) VALUES(?, ?, ?, ?)",
                params![
                    event.event_id,
                    event.room_id,
                    event.body_text,
                    event.formatted_body_html,
                ],
            )?;
        }
        if event.state_key.is_some() {
            self.conn.execute(
                r#"
                INSERT OR IGNORE INTO state_events(room_id, type, state_key, event_id, effective_ts)
                VALUES(?, ?, ?, ?, ?)
                "#,
                params![
                    event.room_id,
                    event.event_type,
                    event.state_key,
                    event.event_id,
                    event.origin_server_ts,
                ],
            )?;
        }
        Ok(())
    }

    pub fn insert_media_ref(&self, media_ref: &MediaRefRecord) -> Result<()> {
        let encrypted = match &media_ref.encrypted_file_json {
            Some(value) => Some(compress_json(value)?),
            None => None,
        };
        self.conn.execute(
            r#"
            INSERT INTO media_refs(event_id, field_path, mxc_uri, object_hash, encrypted_file_json_zstd)
            VALUES(?, ?, ?, ?, ?)
            ON CONFLICT(event_id, field_path, mxc_uri) DO UPDATE SET
                object_hash=COALESCE(excluded.object_hash, media_refs.object_hash),
                encrypted_file_json_zstd=COALESCE(excluded.encrypted_file_json_zstd, media_refs.encrypted_file_json_zstd)
            "#,
            params![
                media_ref.event_id,
                media_ref.field_path,
                media_ref.mxc_uri,
                media_ref.object_hash,
                encrypted,
            ],
        )?;
        Ok(())
    }

    pub fn set_media_object_for_mxc(&self, mxc_uri: &str, object_hash: &str) -> Result<()> {
        self.conn.execute(
            "UPDATE media_refs SET object_hash=? WHERE mxc_uri=?",
            params![object_hash, mxc_uri],
        )?;
        Ok(())
    }

    pub fn insert_media_object(
        &self,
        object_hash: &str,
        size: u64,
        mimetype: Option<&str>,
        original_filename: Option<&str>,
        storage_path: &str,
    ) -> Result<()> {
        self.conn.execute(
            r#"
            INSERT OR IGNORE INTO media_objects(
                object_hash, algo, size, mimetype, original_filename, storage_path, created_at
            ) VALUES(?, 'sha256', ?, ?, ?, ?, ?)
            "#,
            params![
                object_hash,
                size as i64,
                mimetype,
                original_filename,
                storage_path,
                Utc::now().timestamp(),
            ],
        )?;
        Ok(())
    }

    pub fn record_gap(&self, gap: &GapRecord) -> Result<()> {
        self.conn.execute(
            "INSERT INTO gaps(room_id, event_id, kind, detail, created_at) VALUES(?, ?, ?, ?, ?)",
            params![
                gap.room_id,
                gap.event_id,
                gap.kind,
                gap.detail,
                Utc::now().timestamp(),
            ],
        )?;
        Ok(())
    }

    pub fn events_for_room(&self, room_id: &str) -> Result<Vec<EventRecord>> {
        let mut stmt = self.conn.prepare(
            r#"
            SELECT event_id, room_id, origin_server_ts, sender, type, state_key, msgtype,
                   relates_to_event_id, relation_type, redacts_event_id, is_encrypted, is_redacted,
                   body_text, formatted_body_html, raw_event_zstd, decrypted_event_zstd,
                   canonical_sha256, received_at, source_batch
            FROM events
            WHERE room_id=?
            ORDER BY COALESCE(origin_server_ts, received_at), event_id
            "#,
        )?;
        let rows = stmt.query_map([room_id], read_event)?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn all_events(&self) -> Result<Vec<EventRecord>> {
        let mut stmt = self.conn.prepare(
            r#"
            SELECT event_id, room_id, origin_server_ts, sender, type, state_key, msgtype,
                   relates_to_event_id, relation_type, redacts_event_id, is_encrypted, is_redacted,
                   body_text, formatted_body_html, raw_event_zstd, decrypted_event_zstd,
                   canonical_sha256, received_at, source_batch
            FROM events
            ORDER BY room_id, COALESCE(origin_server_ts, received_at), event_id
            "#,
        )?;
        let rows = stmt.query_map([], read_event)?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn media_refs_for_event(&self, event_id: &str) -> Result<Vec<MediaRefRecord>> {
        let mut stmt = self.conn.prepare(
            "SELECT event_id, field_path, mxc_uri, object_hash, encrypted_file_json_zstd FROM media_refs WHERE event_id=?",
        )?;
        let rows = stmt.query_map([event_id], read_media_ref)?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn media_objects(&self) -> Result<Vec<(String, i64, String)>> {
        let mut stmt = self.conn.prepare(
            "SELECT object_hash, size, storage_path FROM media_objects ORDER BY object_hash",
        )?;
        let rows = stmt.query_map([], |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)))?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn checkpoint(&self) -> Result<()> {
        self.conn
            .execute_batch("PRAGMA wal_checkpoint(TRUNCATE);")?;
        Ok(())
    }
}

fn read_room(row: &rusqlite::Row<'_>) -> rusqlite::Result<RoomRecord> {
    Ok(RoomRecord {
        room_id: row.get(0)?,
        name: row.get(1)?,
        canonical_alias: row.get(2)?,
        avatar_mxc: row.get(3)?,
        joined_at: row.get(4)?,
        last_prev_batch: row.get(5)?,
        backfill_token: row.get(6)?,
        backfill_done: row.get::<_, i64>(7)? != 0,
    })
}

fn read_event(row: &rusqlite::Row<'_>) -> rusqlite::Result<EventRecord> {
    let raw: Vec<u8> = row.get(14)?;
    let decrypted: Option<Vec<u8>> = row.get(15)?;
    Ok(EventRecord {
        event_id: row.get(0)?,
        room_id: row.get(1)?,
        origin_server_ts: row.get(2)?,
        sender: row.get(3)?,
        event_type: row.get(4)?,
        state_key: row.get(5)?,
        msgtype: row.get(6)?,
        relates_to_event_id: row.get(7)?,
        relation_type: row.get(8)?,
        redacts_event_id: row.get(9)?,
        is_encrypted: row.get::<_, i64>(10)? != 0,
        is_redacted: row.get::<_, i64>(11)? != 0,
        body_text: row.get(12)?,
        formatted_body_html: row.get(13)?,
        raw_event: decompress_json(&raw).map_err(to_sql_err)?,
        decrypted_event: decrypted
            .as_deref()
            .map(decompress_json)
            .transpose()
            .map_err(to_sql_err)?,
        canonical_sha256: row.get(16)?,
        received_at: row.get(17)?,
        source_batch: row.get(18)?,
    })
}

fn read_media_ref(row: &rusqlite::Row<'_>) -> rusqlite::Result<MediaRefRecord> {
    let encrypted: Option<Vec<u8>> = row.get(4)?;
    Ok(MediaRefRecord {
        event_id: row.get(0)?,
        field_path: row.get(1)?,
        mxc_uri: row.get(2)?,
        object_hash: row.get(3)?,
        encrypted_file_json: encrypted
            .as_deref()
            .map(decompress_json)
            .transpose()
            .map_err(to_sql_err)?,
    })
}

fn compress_json(value: &Value) -> Result<Vec<u8>> {
    let bytes = serde_json::to_vec(value)?;
    zstd::bulk::compress(&bytes, 3).map_err(Into::into)
}

fn decompress_json(bytes: &[u8]) -> Result<Value> {
    let decompressed = zstd::bulk::decompress(bytes, 16 * 1024 * 1024)?;
    serde_json::from_slice(&decompressed).map_err(Into::into)
}

fn to_sql_err(err: anyhow::Error) -> rusqlite::Error {
    rusqlite::Error::FromSqlConversionFailure(
        0,
        rusqlite::types::Type::Blob,
        Box::new(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            err.to_string(),
        )),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn stores_events_idempotently_with_compressed_raw_json() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        let raw = serde_json::json!({
            "event_id": "$one",
            "type": "m.room.message",
            "content": {"body": "hello", "msgtype": "m.text"}
        });
        let event = EventRecord {
            event_id: "$one".into(),
            room_id: "!room:local".into(),
            origin_server_ts: Some(1),
            sender: Some("@a:local".into()),
            event_type: "m.room.message".into(),
            state_key: None,
            msgtype: Some("m.text".into()),
            relates_to_event_id: None,
            relation_type: None,
            redacts_event_id: None,
            is_encrypted: false,
            is_redacted: false,
            body_text: Some("hello".into()),
            formatted_body_html: None,
            raw_event: raw.clone(),
            decrypted_event: None,
            canonical_sha256: "hash".into(),
            received_at: 2,
            source_batch: "sync".into(),
        };
        store.insert_event(&event)?;
        store.insert_event(&event)?;

        let events = store.all_events()?;
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].raw_event, raw);
        Ok(())
    }
}
