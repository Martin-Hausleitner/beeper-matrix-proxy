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
    pub mimetype: Option<String>,
    pub original_filename: Option<String>,
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
            CREATE TABLE IF NOT EXISTS backfill_ranges (
                room_id TEXT NOT NULL REFERENCES rooms(room_id),
                start_token TEXT NOT NULL,
                state TEXT NOT NULL CHECK(state IN ('pending', 'active', 'complete')),
                PRIMARY KEY(room_id, start_token)
            );
            CREATE UNIQUE INDEX IF NOT EXISTS backfill_ranges_active_idx
                ON backfill_ranges(room_id) WHERE state='active';
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
                backfill_token=CASE WHEN rooms.backfill_done=1 THEN rooms.backfill_token
                    ELSE COALESCE(rooms.backfill_token, excluded.backfill_token) END,
                backfill_done=rooms.backfill_done
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

    pub fn enqueue_backfill_range(&self, room_id: &str, start_token: &str) -> Result<()> {
        let tx = self.conn.unchecked_transaction()?;
        let (cursor, done): (Option<String>, bool) = tx.query_row(
            "SELECT backfill_token, backfill_done FROM rooms WHERE room_id=?",
            [room_id],
            |row| Ok((row.get(0)?, row.get(1)?)),
        )?;
        let has_active: bool = tx.query_row(
            "SELECT EXISTS(SELECT 1 FROM backfill_ranges WHERE room_id=? AND state='active')",
            [room_id],
            |row| row.get(0),
        )?;
        // An initial limited sync may already have seeded this exact cursor.
        // Later gaps queue independently; opaque tokens have no ordering.
        let state = if done || (!has_active && cursor.as_deref() == Some(start_token)) {
            "active"
        } else {
            "pending"
        };
        let inserted = tx.execute(
            "INSERT OR IGNORE INTO backfill_ranges(room_id, start_token, state) VALUES(?, ?, ?)",
            params![room_id, start_token, state],
        )?;
        if done && inserted > 0 {
            tx.execute(
                "UPDATE rooms SET backfill_token=?, backfill_done=0 WHERE room_id=?",
                params![start_token, room_id],
            )?;
        }
        tx.commit()?;
        Ok(())
    }

    pub fn update_backfill_cursor(
        &self,
        room_id: &str,
        token: Option<&str>,
        done: bool,
    ) -> Result<Option<String>> {
        let tx = self.conn.unchecked_transaction()?;
        let next_token = if done {
            // Legacy history has no active range row. It still hands off to
            // pending gaps atomically when its current cursor is exhausted.
            tx.execute(
                "UPDATE backfill_ranges SET state='complete' WHERE room_id=? AND state='active'",
                [room_id],
            )?;
            let next: Option<String> = tx.query_row(
                "SELECT start_token FROM backfill_ranges WHERE room_id=? AND state='pending' ORDER BY rowid LIMIT 1",
                [room_id],
                |row| row.get(0),
            ).optional()?;
            if let Some(next) = &next {
                tx.execute(
                    "UPDATE backfill_ranges SET state='active' WHERE room_id=? AND start_token=?",
                    params![room_id, next],
                )?;
            }
            next
        } else {
            token.map(str::to_owned)
        };
        tx.execute(
            "UPDATE rooms SET backfill_token=?, backfill_done=? WHERE room_id=?",
            params![next_token, i64::from(done && next_token.is_none()), room_id],
        )?;
        tx.commit()?;
        Ok(next_token)
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
        let has_redaction: bool = self.conn.query_row(
            "SELECT EXISTS(SELECT 1 FROM events WHERE type='m.room.redaction' AND redacts_event_id=? LIMIT 1)",
            [event.event_id.as_str()],
            |row| row.get::<_, i64>(0).map(|value| value != 0),
        )?;
        if has_redaction && !event.is_redacted {
            self.apply_redaction(&event.event_id)?;
        }
        Ok(())
    }

    pub fn apply_redaction(&self, target_event_id: &str) -> Result<()> {
        self.conn.execute(
            r#"
            UPDATE events
            SET is_redacted=1,
                body_text=NULL,
                formatted_body_html=NULL
            WHERE event_id=?
            "#,
            [target_event_id],
        )?;
        self.conn
            .execute("DELETE FROM event_fts WHERE event_id=?", [target_event_id])?;
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
            r#"
            SELECT mr.event_id, mr.field_path, mr.mxc_uri, mr.object_hash,
                   mo.mimetype, mo.original_filename, mr.encrypted_file_json_zstd
            FROM media_refs mr
            LEFT JOIN media_objects mo ON mo.object_hash = mr.object_hash
            WHERE mr.event_id=?
            ORDER BY mr.field_path, mr.mxc_uri
            "#,
        )?;
        let rows = stmt.query_map([event_id], read_media_ref)?;
        rows.collect::<rusqlite::Result<Vec<_>>>()
            .map_err(Into::into)
    }

    pub fn media_object_hash_for_mxc(&self, mxc_uri: &str) -> Result<Option<String>> {
        self.conn
            .query_row(
                "SELECT object_hash FROM media_refs WHERE mxc_uri=? AND object_hash IS NOT NULL LIMIT 1",
                [mxc_uri],
                |row| row.get(0),
            )
            .optional()
            .map_err(Into::into)
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
    let encrypted: Option<Vec<u8>> = row.get(6)?;
    Ok(MediaRefRecord {
        event_id: row.get(0)?,
        field_path: row.get(1)?,
        mxc_uri: row.get(2)?,
        object_hash: row.get(3)?,
        mimetype: row.get(4)?,
        original_filename: row.get(5)?,
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

    fn backfill_room() -> RoomRecord {
        RoomRecord {
            room_id: "!room:local".into(),
            name: None,
            canonical_alias: None,
            avatar_mxc: None,
            joined_at: None,
            last_prev_batch: Some("initial".into()),
            backfill_token: Some("initial".into()),
            backfill_done: false,
        }
    }

    #[test]
    fn range_handoff_rolls_back_if_room_cursor_update_fails() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.upsert_room(&backfill_room())?;
        store.enqueue_backfill_range("!room:local", "initial")?;
        store.enqueue_backfill_range("!room:local", "next-gap")?;
        store.conn.execute_batch("CREATE TRIGGER fail_cursor_update BEFORE UPDATE ON rooms BEGIN SELECT RAISE(ABORT, 'fixture failure'); END;")?;
        assert!(store
            .update_backfill_cursor("!room:local", None, true)
            .is_err());
        let state = |token: &str| -> Result<String> {
            Ok(store.conn.query_row(
                "SELECT state FROM backfill_ranges WHERE room_id=? AND start_token=?",
                params!["!room:local", token],
                |row| row.get(0),
            )?)
        };
        assert_eq!(state("initial")?, "active");
        assert_eq!(state("next-gap")?, "pending");
        assert_eq!(
            store.rooms_for_backfill(1)?[0].backfill_token.as_deref(),
            Some("initial")
        );
        store
            .conn
            .execute_batch("DROP TRIGGER fail_cursor_update;")?;
        assert_eq!(
            store
                .update_backfill_cursor("!room:local", None, true)?
                .as_deref(),
            Some("next-gap")
        );
        assert_eq!(state("initial")?, "complete");
        assert_eq!(state("next-gap")?, "active");
        Ok(())
    }

    #[test]
    fn additive_migration_preserves_legacy_cursor_and_accepts_new_gap() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.upsert_room(&backfill_room())?;
        store.update_backfill_cursor("!room:local", Some("legacy-progress"), false)?;
        store.conn.execute_batch("DROP TABLE backfill_ranges;")?;
        drop(store);
        let store = ArchiveStore::open(dir.path())?;
        assert_eq!(
            store.rooms_for_backfill(1)?[0].backfill_token.as_deref(),
            Some("legacy-progress")
        );
        store.enqueue_backfill_range("!room:local", "new-gap")?;
        assert_eq!(
            store.rooms_for_backfill(1)?[0].backfill_token.as_deref(),
            Some("legacy-progress")
        );
        assert_eq!(
            store
                .update_backfill_cursor("!room:local", None, true)?
                .as_deref(),
            Some("new-gap")
        );
        Ok(())
    }

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

    #[test]
    fn sync_upsert_does_not_mark_existing_room_backfill_complete() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.upsert_room(&RoomRecord {
            room_id: "!room:local".into(),
            name: Some("Room".into()),
            canonical_alias: None,
            avatar_mxc: None,
            joined_at: Some(1),
            last_prev_batch: Some("prev".into()),
            backfill_token: Some("prev".into()),
            backfill_done: false,
        })?;

        store.upsert_room(&RoomRecord {
            room_id: "!room:local".into(),
            name: Some("Room renamed".into()),
            canonical_alias: None,
            avatar_mxc: None,
            joined_at: Some(2),
            last_prev_batch: None,
            backfill_token: None,
            backfill_done: true,
        })?;

        let rooms = store.rooms_for_backfill(10)?;
        assert_eq!(rooms.len(), 1);
        assert_eq!(rooms[0].room_id, "!room:local");
        assert_eq!(rooms[0].backfill_token.as_deref(), Some("prev"));
        assert!(!rooms[0].backfill_done);
        Ok(())
    }

    #[test]
    fn redaction_marks_target_event_deleted_and_unsearchable() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        let event = EventRecord {
            event_id: "$target".into(),
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
            body_text: Some("secret body".into()),
            formatted_body_html: Some("<b>secret body</b>".into()),
            raw_event: serde_json::json!({"event_id":"$target","type":"m.room.message","content":{"body":"secret body"}}),
            decrypted_event: None,
            canonical_sha256: "hash".into(),
            received_at: 2,
            source_batch: "sync".into(),
        };
        store.insert_event(&event)?;
        store.apply_redaction("$target")?;

        let events = store.all_events()?;
        assert_eq!(events.len(), 1);
        assert!(events[0].is_redacted);
        assert!(events[0].body_text.is_none());
        assert!(events[0].formatted_body_html.is_none());
        Ok(())
    }
}
