use std::path::PathBuf;
use std::time::Duration;

use anyhow::{Context, Result};
use chrono::Utc;
use serde_json::Value;
use sha2::{Digest, Sha256};
use tokio::time::sleep;

use crate::cli::{BackfillArgs, RepairMediaArgs, SyncArgs};
use crate::matrix::MatrixClient;
use crate::media::{extract_mxc_references, sha256_hex, MediaStore, MxcReference};
use crate::store::{ArchiveStore, EventRecord, GapRecord, MediaRefRecord, RoomRecord};

pub struct Archiver {
    client: MatrixClient,
    store: ArchiveStore,
    media_store: MediaStore,
}

impl Archiver {
    pub fn new(client: MatrixClient, store: ArchiveStore, archive_dir: PathBuf) -> Self {
        let media_store = MediaStore::new(&archive_dir);
        Self {
            client,
            store,
            media_store,
        }
    }

    pub async fn sync(&self, args: SyncArgs) -> Result<()> {
        let mut passes = 0;
        loop {
            let since = self.store.get_state("sync_next_batch")?;
            let response = self
                .client
                .sync(since.as_deref(), args.timeout_ms, args.timeline_limit)
                .await?;
            self.ingest_sync_response(&response, args.download_media)
                .await?;
            if let Some(next_batch) = response.get("next_batch").and_then(Value::as_str) {
                self.store.set_state("sync_next_batch", next_batch)?;
            }
            if args.refresh_room_state {
                self.refresh_joined_room_state(args.download_media).await?;
            }
            passes += 1;
            if !args.follow && passes >= args.passes {
                return Ok(());
            }
            sleep(Duration::from_millis(250)).await;
        }
    }

    pub async fn backfill(&self, args: BackfillArgs) -> Result<()> {
        let rooms = self.store.rooms_for_backfill(args.room_limit)?;
        for room in rooms {
            let mut token = room
                .backfill_token
                .clone()
                .or(room.last_prev_batch.clone())
                .with_context(|| format!("room {} has no backfill token", room.room_id))?;
            let mut batches = 0;
            loop {
                if args.max_batches_per_room > 0 && batches >= args.max_batches_per_room {
                    break;
                }
                let response = self
                    .client
                    .messages(&room.room_id, &token, args.batch_limit)
                    .await
                    .with_context(|| format!("backfill room {}", room.room_id))?;
                self.ingest_messages_response(&room.room_id, &response, args.download_media)
                    .await?;
                batches += 1;
                let end = response.get("end").and_then(Value::as_str);
                match end {
                    Some(next) if next != token => {
                        token = next.to_owned();
                        self.store
                            .update_backfill_cursor(&room.room_id, Some(&token), false)?;
                    }
                    Some(_) => {
                        self.store.record_gap(&GapRecord {
                            room_id: Some(room.room_id.clone()),
                            event_id: None,
                            kind: "stalled_backfill_cursor".into(),
                            detail: "messages response did not advance its cursor; range remains incomplete".into(),
                        })?;
                        anyhow::bail!(
                            "backfill cursor did not advance for room {}; range held",
                            room.room_id
                        );
                    }
                    None => {
                        match self
                            .store
                            .update_backfill_cursor(&room.room_id, None, true)?
                        {
                            Some(next) => token = next,
                            None => break,
                        }
                    }
                }
            }
        }
        Ok(())
    }

    pub async fn repair_media_refs(&self, args: RepairMediaArgs) -> Result<()> {
        for event in self.store.all_events()? {
            for media_ref in extract_mxc_references(&event.raw_event) {
                self.ingest_media_ref(
                    &event.room_id,
                    &event.event_id,
                    &media_ref,
                    args.download_media,
                )
                .await?;
            }
        }
        Ok(())
    }

    async fn refresh_joined_room_state(&self, download_media: bool) -> Result<()> {
        let rooms = self.client.joined_rooms().await?;
        for room_id in rooms {
            let response = self.client.room_state(&room_id).await?;
            let state_events = response.as_array().cloned().unwrap_or_default();
            let room_record = room_record_from_state(&room_id, &state_events, None);
            self.store.upsert_room(&room_record)?;
            for event in state_events {
                self.ingest_event(&room_id, &event, "room_state", download_media)
                    .await?;
            }
        }
        Ok(())
    }

    async fn ingest_sync_response(&self, response: &Value, download_media: bool) -> Result<()> {
        let source_batch = response
            .get("next_batch")
            .and_then(Value::as_str)
            .unwrap_or("sync")
            .to_owned();
        let Some(joined) = response
            .get("rooms")
            .and_then(|rooms| rooms.get("join"))
            .and_then(Value::as_object)
        else {
            return Ok(());
        };

        for (room_id, room) in joined {
            let state_events = room
                .get("state")
                .and_then(|state| state.get("events"))
                .and_then(Value::as_array)
                .cloned()
                .unwrap_or_default();
            let timeline = room.get("timeline").unwrap_or(&Value::Null);
            let timeline_events = timeline
                .get("events")
                .and_then(Value::as_array)
                .cloned()
                .unwrap_or_default();
            let prev_batch = timeline.get("prev_batch").and_then(Value::as_str);
            let room_record = room_record_from_state(room_id, &state_events, prev_batch);
            self.store.upsert_room(&room_record)?;

            for event in state_events.iter().chain(timeline_events.iter()) {
                self.ingest_event(room_id, event, &source_batch, download_media)
                    .await?;
            }
            if timeline
                .get("limited")
                .and_then(Value::as_bool)
                .unwrap_or(false)
            {
                if let Some(token) = prev_batch {
                    self.store.enqueue_backfill_range(room_id, token)?;
                }
                self.store.record_gap(&GapRecord {
                    room_id: Some(room_id.clone()),
                    event_id: None,
                    kind: "limited_timeline".into(),
                    detail: "sync response marked timeline as limited; backfill required".into(),
                })?;
            }
        }
        Ok(())
    }

    async fn ingest_messages_response(
        &self,
        room_id: &str,
        response: &Value,
        download_media: bool,
    ) -> Result<()> {
        let source_batch = response
            .get("start")
            .and_then(Value::as_str)
            .unwrap_or("messages")
            .to_owned();
        let state_events = response
            .get("state")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        let chunk = response
            .get("chunk")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        for event in state_events.iter().chain(chunk.iter()) {
            self.ingest_event(room_id, event, &source_batch, download_media)
                .await?;
        }
        if chunk.is_empty() && response.get("end").is_some() {
            self.store.record_gap(&GapRecord {
                room_id: Some(room_id.to_owned()),
                event_id: None,
                kind: "empty_backfill_chunk".into(),
                detail: "messages response returned no events but still had an end token".into(),
            })?;
        }
        Ok(())
    }

    async fn ingest_event(
        &self,
        room_id: &str,
        event: &Value,
        source_batch: &str,
        download_media: bool,
    ) -> Result<()> {
        let Some(event_id) = event.get("event_id").and_then(Value::as_str) else {
            self.store.record_gap(&GapRecord {
                room_id: Some(room_id.to_owned()),
                event_id: None,
                kind: "event_without_id".into(),
                detail: event.to_string(),
            })?;
            return Ok(());
        };
        let record = event_record_from_json(room_id, event, source_batch);
        self.store.insert_event(&record)?;
        if record.event_type == "m.room.redaction" {
            if let Some(target_event_id) = record.redacts_event_id.as_deref() {
                self.store.apply_redaction(target_event_id)?;
            }
        }

        let media_refs = extract_mxc_references(event);
        for media_ref in media_refs {
            self.ingest_media_ref(room_id, event_id, &media_ref, download_media)
                .await?;
        }
        if record.is_encrypted {
            self.store.record_gap(&GapRecord {
                room_id: Some(room_id.to_owned()),
                event_id: Some(event_id.to_owned()),
                kind: "undecrypted_e2ee".into(),
                detail: "raw m.room.encrypted event stored; plaintext unavailable in v1".into(),
            })?;
        }
        Ok(())
    }

    async fn ingest_media_ref(
        &self,
        room_id: &str,
        event_id: &str,
        media_ref: &MxcReference,
        download_media: bool,
    ) -> Result<()> {
        let existing_hash = self.store.media_object_hash_for_mxc(&media_ref.mxc_uri)?;
        self.store.insert_media_ref(&MediaRefRecord {
            event_id: event_id.to_owned(),
            field_path: media_ref.field_path.clone(),
            mxc_uri: media_ref.mxc_uri.clone(),
            object_hash: existing_hash.clone(),
            mimetype: None,
            original_filename: None,
            encrypted_file_json: media_ref.encrypted_file_json.clone(),
        })?;
        if !download_media || existing_hash.is_some() {
            return Ok(());
        }
        match self.client.download_media(&media_ref.mxc_uri).await {
            Ok(downloaded) => {
                let hash = sha256_hex(&downloaded.bytes);
                let stored = self.media_store.store(&downloaded)?;
                self.store.insert_media_object(
                    &stored.hash,
                    stored.size,
                    downloaded.mimetype.as_deref(),
                    None,
                    &stored.relative_path,
                )?;
                self.store
                    .set_media_object_for_mxc(&media_ref.mxc_uri, &hash)?;
            }
            Err(err) => {
                self.store.record_gap(&GapRecord {
                    room_id: Some(room_id.to_owned()),
                    event_id: Some(event_id.to_owned()),
                    kind: "media_unavailable".into(),
                    detail: format!("{}: {}", media_ref.mxc_uri, err),
                })?;
            }
        }
        Ok(())
    }
}

fn room_record_from_state(
    room_id: &str,
    state_events: &[Value],
    prev_batch: Option<&str>,
) -> RoomRecord {
    let mut name = None;
    let mut canonical_alias = None;
    let mut avatar_mxc = None;
    for event in state_events {
        let event_type = event.get("type").and_then(Value::as_str);
        let content = event.get("content").unwrap_or(&Value::Null);
        match event_type {
            Some("m.room.name") => {
                name = content
                    .get("name")
                    .and_then(Value::as_str)
                    .map(str::to_owned)
            }
            Some("m.room.canonical_alias") => {
                canonical_alias = content
                    .get("alias")
                    .and_then(Value::as_str)
                    .map(str::to_owned);
            }
            Some("m.room.avatar") => {
                avatar_mxc = content
                    .get("url")
                    .and_then(Value::as_str)
                    .map(str::to_owned);
            }
            _ => {}
        }
    }
    RoomRecord {
        room_id: room_id.to_owned(),
        name,
        canonical_alias,
        avatar_mxc,
        joined_at: Some(Utc::now().timestamp()),
        last_prev_batch: prev_batch.map(str::to_owned),
        backfill_token: prev_batch.map(str::to_owned),
        backfill_done: false,
    }
}

fn event_record_from_json(room_id: &str, event: &Value, source_batch: &str) -> EventRecord {
    let content = event.get("content").unwrap_or(&Value::Null);
    let event_type = event
        .get("type")
        .and_then(Value::as_str)
        .unwrap_or("unknown")
        .to_owned();
    let relation = content.get("m.relates_to").unwrap_or(&Value::Null);
    let relation_type = relation
        .get("rel_type")
        .and_then(Value::as_str)
        .map(str::to_owned);
    let relates_to_event_id = relation
        .get("event_id")
        .and_then(Value::as_str)
        .or_else(|| {
            content
                .get("m.relates_to")
                .and_then(|rel| rel.get("m.in_reply_to"))
                .and_then(|reply| reply.get("event_id"))
                .and_then(Value::as_str)
        })
        .map(str::to_owned);
    let redacts_event_id = event
        .get("redacts")
        .and_then(Value::as_str)
        .or_else(|| content.get("redacts").and_then(Value::as_str))
        .map(str::to_owned);
    EventRecord {
        event_id: event
            .get("event_id")
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_owned(),
        room_id: room_id.to_owned(),
        origin_server_ts: event.get("origin_server_ts").and_then(Value::as_i64),
        sender: event
            .get("sender")
            .and_then(Value::as_str)
            .map(str::to_owned),
        event_type: event_type.clone(),
        state_key: event
            .get("state_key")
            .and_then(Value::as_str)
            .map(str::to_owned),
        msgtype: content
            .get("msgtype")
            .and_then(Value::as_str)
            .map(str::to_owned),
        relates_to_event_id,
        relation_type,
        redacts_event_id,
        is_encrypted: event_type == "m.room.encrypted",
        is_redacted: event_type == "m.room.redaction",
        body_text: content
            .get("body")
            .and_then(Value::as_str)
            .map(str::to_owned),
        formatted_body_html: content
            .get("formatted_body")
            .and_then(Value::as_str)
            .map(str::to_owned),
        raw_event: event.clone(),
        decrypted_event: None,
        canonical_sha256: canonical_hash(event),
        received_at: Utc::now().timestamp(),
        source_batch: source_batch.to_owned(),
    }
}

fn canonical_hash(value: &Value) -> String {
    let bytes = serde_json::to_vec(value).unwrap_or_default();
    let mut hasher = Sha256::new();
    hasher.update(bytes);
    hex::encode(hasher.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_archiver(dir: &std::path::Path) -> Result<Archiver> {
        Ok(Archiver::new(
            MatrixClient::from_options(
                crate::cli::MatrixOptions {
                    homeserver: "http://127.0.0.1:9".into(),
                    access_token: "synthetic-test-token".into(),
                },
                false,
            )?,
            ArchiveStore::open(dir)?,
            dir.to_owned(),
        ))
    }

    fn sync_fixture(token: &str, limited: bool) -> Value {
        serde_json::json!({"next_batch": token, "rooms": {"join": {
            "!room:local": {"timeline": {"prev_batch": token, "limited": limited, "events": []}}
        }}})
    }

    fn message_fixture_server(response: Value) -> Result<(String, std::thread::JoinHandle<()>)> {
        use std::io::{Read, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0")?;
        let address = format!("http://{}", listener.local_addr()?);
        listener.set_nonblocking(true)?;
        let worker = std::thread::spawn(move || {
            let deadline = std::time::Instant::now() + Duration::from_secs(5);
            let mut socket = loop {
                match listener.accept() {
                    Ok((socket, _)) => break socket,
                    Err(err) if err.kind() == std::io::ErrorKind::WouldBlock => {
                        assert!(
                            std::time::Instant::now() < deadline,
                            "fixture received no request"
                        );
                        std::thread::sleep(Duration::from_millis(5));
                    }
                    Err(err) => panic!("fixture accept failed: {err}"),
                }
            };
            socket
                .set_read_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            let mut request = Vec::new();
            let mut byte = [0; 1];
            while !request.ends_with(b"\r\n\r\n") {
                socket.read_exact(&mut byte).unwrap();
                request.push(byte[0]);
                assert!(request.len() < 16384);
            }
            assert!(String::from_utf8_lossy(&request).contains("/messages?"));
            let body = response.to_string();
            write!(socket, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}", body.len(), body).unwrap();
        });
        Ok((address, worker))
    }

    fn backfill_args(homeserver: String) -> BackfillArgs {
        BackfillArgs {
            matrix: crate::cli::MatrixOptions {
                homeserver,
                access_token: "synthetic-test-token".into(),
            },
            batch_limit: 10,
            max_batches_per_room: 1,
            room_limit: 1,
            download_media: false,
            insecure_tls: false,
        }
    }

    #[tokio::test]
    async fn unchanged_end_cursor_holds_range_instead_of_completing() -> Result<()> {
        let dir = tempfile::tempdir()?;
        let (address, server) = message_fixture_server(serde_json::json!({
            "start": "initial", "end": "initial", "chunk": []
        }))?;
        let args = backfill_args(address);
        let archiver = Archiver::new(
            MatrixClient::from_options(args.matrix.clone(), false)?,
            ArchiveStore::open(dir.path())?,
            dir.path().to_owned(),
        );
        archiver
            .ingest_sync_response(&sync_fixture("initial", false), false)
            .await?;
        let result = archiver.backfill(args).await;
        server.join().unwrap();
        assert!(
            result.is_err(),
            "an unchanged cursor is not proof of completed history"
        );
        let rooms = archiver.store.rooms_for_backfill(10)?;
        assert_eq!(rooms.len(), 1);
        assert_eq!(rooms[0].backfill_token.as_deref(), Some("initial"));
        assert!(!rooms[0].backfill_done);
        Ok(())
    }

    #[tokio::test]
    async fn completed_range_consumes_batch_budget_before_next_range() -> Result<()> {
        let dir = tempfile::tempdir()?;
        let (address, server) = message_fixture_server(serde_json::json!({
            "start": "initial", "chunk": []
        }))?;
        let args = backfill_args(address);
        let archiver = Archiver::new(
            MatrixClient::from_options(args.matrix.clone(), false)?,
            ArchiveStore::open(dir.path())?,
            dir.path().to_owned(),
        );
        archiver
            .ingest_sync_response(&sync_fixture("initial", false), false)
            .await?;
        archiver
            .ingest_sync_response(&sync_fixture("new-gap", true), false)
            .await?;
        let result = archiver.backfill(args).await;
        server.join().unwrap();
        result?;
        let rooms = archiver.store.rooms_for_backfill(10)?;
        assert_eq!(rooms.len(), 1);
        assert_eq!(rooms[0].backfill_token.as_deref(), Some("new-gap"));
        Ok(())
    }

    #[tokio::test]
    async fn sync_preserves_in_progress_history_cursor() -> Result<()> {
        let dir = tempfile::tempdir()?;
        let archiver = test_archiver(dir.path())?;
        archiver
            .ingest_sync_response(&sync_fixture("initial", false), false)
            .await?;
        archiver
            .store
            .update_backfill_cursor("!room:local", Some("older-progress"), false)?;
        archiver
            .ingest_sync_response(&sync_fixture("recent-sync", false), false)
            .await?;
        let rooms = archiver.store.rooms_for_backfill(10)?;
        assert_eq!(rooms[0].backfill_token.as_deref(), Some("older-progress"));
        assert_eq!(rooms[0].last_prev_batch.as_deref(), Some("recent-sync"));
        Ok(())
    }

    #[tokio::test]
    async fn limited_sync_reopens_completed_history_and_deduplicates_replay() -> Result<()> {
        let dir = tempfile::tempdir()?;
        let archiver = test_archiver(dir.path())?;
        archiver
            .ingest_sync_response(&sync_fixture("initial", false), false)
            .await?;
        archiver
            .store
            .update_backfill_cursor("!room:local", None, true)?;
        archiver
            .ingest_sync_response(&sync_fixture("new-gap", true), false)
            .await?;
        let rooms = archiver.store.rooms_for_backfill(10)?;
        assert_eq!(
            rooms.len(),
            1,
            "a later limited sync must reopen history work"
        );
        assert_eq!(rooms[0].backfill_token.as_deref(), Some("new-gap"));
        archiver
            .store
            .update_backfill_cursor("!room:local", None, true)?;
        archiver
            .ingest_sync_response(&sync_fixture("new-gap", true), false)
            .await?;
        assert!(archiver.store.rooms_for_backfill(10)?.is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn limited_ranges_survive_restart_without_replacing_active_history() -> Result<()> {
        let dir = tempfile::tempdir()?;
        let archiver = test_archiver(dir.path())?;
        archiver
            .ingest_sync_response(&sync_fixture("initial", false), false)
            .await?;
        archiver
            .store
            .update_backfill_cursor("!room:local", Some("older-progress"), false)?;
        for token in ["gap-a", "gap-b"] {
            archiver
                .ingest_sync_response(&sync_fixture(token, true), false)
                .await?;
        }
        assert_eq!(
            archiver.store.rooms_for_backfill(10)?[0]
                .backfill_token
                .as_deref(),
            Some("older-progress")
        );
        drop(archiver);
        let archiver = test_archiver(dir.path())?;
        for expected in ["gap-a", "gap-b"] {
            archiver
                .store
                .update_backfill_cursor("!room:local", None, true)?;
            assert_eq!(
                archiver.store.rooms_for_backfill(10)?[0]
                    .backfill_token
                    .as_deref(),
                Some(expected)
            );
            archiver
                .store
                .update_backfill_cursor("!room:local", Some("range-progress"), false)?;
        }
        archiver
            .store
            .update_backfill_cursor("!room:local", None, true)?;
        assert!(archiver.store.rooms_for_backfill(10)?.is_empty());
        Ok(())
    }

    #[test]
    fn extracts_reply_relation_from_event() {
        let event = serde_json::json!({
            "event_id": "$reply",
            "type": "m.room.message",
            "content": {
                "body": "reply",
                "m.relates_to": {"m.in_reply_to": {"event_id": "$parent"}}
            }
        });
        let record = event_record_from_json("!room:local", &event, "batch");
        assert_eq!(record.relates_to_event_id.as_deref(), Some("$parent"));
    }

    #[test]
    fn uses_room_name_state_for_room_record() {
        let state = vec![serde_json::json!({
            "type": "m.room.name",
            "content": {"name": "Archive Me"}
        })];
        let room = room_record_from_state("!room:local", &state, Some("prev"));
        assert_eq!(room.name.as_deref(), Some("Archive Me"));
        assert_eq!(room.backfill_token.as_deref(), Some("prev"));
        assert!(!room.backfill_done);
    }

    #[test]
    fn sync_room_record_without_prev_batch_does_not_complete_backfill() {
        let room = room_record_from_state("!room:local", &[], None);
        assert_eq!(room.backfill_token, None);
        assert!(!room.backfill_done);
    }
}
