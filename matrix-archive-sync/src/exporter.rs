use std::fs;
use std::io::{BufWriter, Write};
use std::path::Path;

use anyhow::{Context, Result};
use html_escape::{encode_double_quoted_attribute, encode_text};
use serde::Serialize;

use crate::store::{ArchiveStore, EventRecord, MediaRefRecord, RoomRecord};

const BEEPER_PROFILE_AVATAR_FIELD: &str = "content.com.beeper.per_message_profile.avatar_url";

pub fn export_jsonl(store: &ArchiveStore, output: &Path) -> Result<()> {
    if let Some(parent) = output.parent() {
        fs::create_dir_all(parent)?;
    }
    let file = fs::File::create(output)
        .with_context(|| format!("create JSONL export {}", output.display()))?;
    let mut writer = BufWriter::new(file);
    for event in store.all_events()? {
        let media_refs = store.media_refs_for_event(&event.event_id)?;
        let row = JsonlEvent { event, media_refs };
        serde_json::to_writer(&mut writer, &row)?;
        writer.write_all(b"\n")?;
    }
    Ok(())
}

pub fn export_html(store: &ArchiveStore, archive_dir: &Path, output_dir: &Path) -> Result<()> {
    fs::create_dir_all(output_dir)?;
    let rooms = store.list_rooms()?;
    let mut index = String::from("<!doctype html><meta charset=\"utf-8\"><title>Matrix Archive</title><h1>Matrix Archive</h1><ul>");
    for room in &rooms {
        let file_name = room_file_name(&room.room_id);
        index.push_str("<li><a href=\"rooms/");
        index.push_str(&file_name);
        index.push_str("\">");
        index.push_str(&encode_text(&room_display_name(room)));
        index.push_str("</a></li>");
    }
    index.push_str("</ul>");
    fs::create_dir_all(output_dir.join("rooms"))?;
    fs::write(output_dir.join("index.html"), index)?;

    for room in rooms {
        let events = store.events_for_room(&room.room_id)?;
        let mut html = String::new();
        html.push_str("<!doctype html><meta charset=\"utf-8\"><link rel=\"stylesheet\" href=\"../style.css\">");
        html.push_str("<title>");
        html.push_str(&encode_text(&room_display_name(&room)));
        html.push_str("</title><h1>");
        html.push_str(&encode_text(&room_display_name(&room)));
        html.push_str("</h1><main>");
        for event in events {
            let media_refs = store.media_refs_for_event(&event.event_id)?;
            render_event(&mut html, archive_dir, output_dir, &event, &media_refs)?;
        }
        html.push_str("</main>");
        fs::write(
            output_dir.join("rooms").join(room_file_name(&room.room_id)),
            html,
        )?;
    }
    fs::write(
        output_dir.join("style.css"),
        r#"
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem; line-height: 1.45; }
        main { max-width: 980px; }
        article { border-bottom: 1px solid #ddd; padding: .75rem 0; }
        .event-head { display: flex; gap: .65rem; align-items: flex-start; }
        .sender-avatar { width: 34px; height: 34px; border-radius: 50%; flex: 0 0 34px; object-fit: cover; background: #ececec; color: #555; display: grid; place-items: center; font-weight: 700; font-size: .85rem; overflow: hidden; }
        .sender-avatar img { width: 100%; height: 100%; object-fit: cover; display: block; }
        .event-content { min-width: 0; flex: 1; }
        .meta { color: #666; font-size: .9rem; }
        .marker { display: inline-block; border: 1px solid #bbb; border-radius: 4px; padding: 0 .25rem; margin-left: .25rem; color: #555; font-size: .8rem; }
        .body { white-space: pre-wrap; margin-top: .35rem; }
        .media { margin-top: .45rem; }
        .media img, .media video { max-width: min(620px, 100%); max-height: 520px; border-radius: 6px; display: block; }
        .media audio { width: min(620px, 100%); display: block; }
        "#,
    )?;
    Ok(())
}

#[derive(Serialize)]
struct JsonlEvent {
    event: EventRecord,
    media_refs: Vec<MediaRefRecord>,
}

fn render_event(
    html: &mut String,
    archive_dir: &Path,
    output_dir: &Path,
    event: &EventRecord,
    media_refs: &[MediaRefRecord],
) -> Result<()> {
    html.push_str("<article id=\"");
    html.push_str(&encode_text(&event.event_id));
    html.push_str("\"><div class=\"event-head\">");
    render_sender_avatar(html, archive_dir, output_dir, event, media_refs)?;
    html.push_str("<div class=\"event-content\"><div class=\"meta\">");
    let profile = beeper_profile(event);
    let sender_label = profile
        .display_name
        .as_deref()
        .or(event.sender.as_deref())
        .unwrap_or("unknown sender");
    html.push_str(&encode_text(sender_label));
    html.push_str(" · ");
    html.push_str(&encode_text(&timestamp_label(event.origin_server_ts)));
    html.push_str(" · ");
    html.push_str(&encode_text(&event.event_type));
    if event.is_encrypted {
        html.push_str("<span class=\"marker\">undecrypted E2EE</span>");
    }
    if event.is_redacted {
        html.push_str("<span class=\"marker\">redaction</span>");
    }
    if let Some(rel) = &event.relation_type {
        html.push_str("<span class=\"marker\">");
        html.push_str(&encode_text(rel));
        html.push_str("</span>");
    }
    html.push_str("</div>");
    if let Some(body) = &event.body_text {
        html.push_str("<div class=\"body\">");
        html.push_str(&encode_text(body));
        html.push_str("</div>");
    } else if event.is_redacted {
        html.push_str("<div class=\"body\"><em>redacted or deleted</em></div>");
    }
    if event.is_redacted && !media_refs.is_empty() {
        html.push_str(
            "<div class=\"media\"><span class=\"marker\">redacted media hidden</span></div>",
        );
    }
    for media_ref in media_refs
        .iter()
        .filter(|media_ref| !event.is_redacted && !is_sender_avatar_ref(media_ref))
    {
        html.push_str("<div class=\"media\">");
        if let Some(object_hash) = &media_ref.object_hash {
            let relative = object_relative_link(archive_dir, output_dir, object_hash)?;
            render_media_object(html, event, media_ref, &relative);
        } else {
            html.push_str("<span class=\"marker\">missing media</span> ");
            html.push_str(&encode_text(&media_ref.mxc_uri));
        }
        html.push_str("</div>");
    }
    html.push_str("</div></div></article>");
    Ok(())
}

fn render_sender_avatar(
    html: &mut String,
    archive_dir: &Path,
    output_dir: &Path,
    event: &EventRecord,
    media_refs: &[MediaRefRecord],
) -> Result<()> {
    let profile = beeper_profile(event);
    let label = profile
        .display_name
        .as_deref()
        .or(event.sender.as_deref())
        .unwrap_or("?");
    let avatar_ref = media_refs
        .iter()
        .find(|media_ref| is_sender_avatar_ref(media_ref) && media_ref.object_hash.is_some());
    html.push_str("<div class=\"sender-avatar\" title=\"");
    html.push_str(&encode_double_quoted_attribute(label));
    html.push_str("\">");
    if let Some(media_ref) = avatar_ref {
        if let Some(object_hash) = &media_ref.object_hash {
            let relative = object_relative_link(archive_dir, output_dir, object_hash)?;
            let href = encode_double_quoted_attribute(&relative);
            html.push_str("<img loading=\"lazy\" src=\"");
            html.push_str(&href);
            html.push_str("\" alt=\"");
            html.push_str(&encode_double_quoted_attribute(label));
            html.push_str("\">");
        }
    } else {
        html.push_str(&encode_text(&avatar_initials(label)));
    }
    html.push_str("</div>");
    Ok(())
}

#[derive(Default)]
struct BeeperProfile {
    display_name: Option<String>,
}

fn beeper_profile(event: &EventRecord) -> BeeperProfile {
    let Some(profile) = event
        .raw_event
        .get("content")
        .and_then(|content| content.get("com.beeper.per_message_profile"))
    else {
        return BeeperProfile::default();
    };
    BeeperProfile {
        display_name: profile
            .get("displayname")
            .and_then(serde_json::Value::as_str)
            .map(str::to_owned),
    }
}

fn is_sender_avatar_ref(media_ref: &MediaRefRecord) -> bool {
    media_ref.field_path == BEEPER_PROFILE_AVATAR_FIELD
}

fn avatar_initials(label: &str) -> String {
    label
        .split_whitespace()
        .filter_map(|part| part.chars().next())
        .take(2)
        .collect::<String>()
        .to_uppercase()
}

fn render_media_object(
    html: &mut String,
    event: &EventRecord,
    media_ref: &MediaRefRecord,
    relative: &str,
) {
    let href = encode_double_quoted_attribute(relative);
    let label = media_ref
        .original_filename
        .as_deref()
        .unwrap_or(&media_ref.mxc_uri);
    let alt = encode_double_quoted_attribute(label);
    let mime = media_ref.mimetype.as_deref().unwrap_or_default();
    let msgtype = event.msgtype.as_deref().unwrap_or_default();
    if mime.starts_with("image/") || msgtype == "m.image" {
        html.push_str("<a href=\"");
        html.push_str(&href);
        html.push_str("\"><img loading=\"lazy\" src=\"");
        html.push_str(&href);
        html.push_str("\" alt=\"");
        html.push_str(&alt);
        html.push_str("\"></a>");
        return;
    }
    if mime.starts_with("video/") || msgtype == "m.video" {
        html.push_str("<video controls preload=\"metadata\" src=\"");
        html.push_str(&href);
        html.push_str("\"></video><a href=\"");
        html.push_str(&href);
        html.push_str("\">");
        html.push_str(&encode_text(label));
        html.push_str("</a>");
        return;
    }
    if mime.starts_with("audio/") || msgtype == "m.audio" {
        html.push_str("<audio controls preload=\"metadata\" src=\"");
        html.push_str(&href);
        html.push_str("\"></audio><a href=\"");
        html.push_str(&href);
        html.push_str("\">");
        html.push_str(&encode_text(label));
        html.push_str("</a>");
        return;
    }
    html.push_str("<a href=\"");
    html.push_str(&href);
    html.push_str("\">");
    html.push_str(&encode_text(label));
    html.push_str("</a>");
}

fn room_display_name(room: &RoomRecord) -> String {
    room.name
        .clone()
        .or_else(|| room.canonical_alias.clone())
        .unwrap_or_else(|| room.room_id.clone())
}

fn room_file_name(room_id: &str) -> String {
    let sanitized: String = room_id
        .chars()
        .map(|ch| if ch.is_ascii_alphanumeric() { ch } else { '_' })
        .collect();
    format!("{sanitized}.html")
}

fn timestamp_label(ts: Option<i64>) -> String {
    let Some(ms) = ts else {
        return "unknown time".into();
    };
    chrono::DateTime::from_timestamp_millis(ms)
        .map(|dt| dt.to_rfc3339())
        .unwrap_or_else(|| ms.to_string())
}

fn object_relative_link(
    archive_dir: &Path,
    output_dir: &Path,
    object_hash: &str,
) -> Result<String> {
    let object = archive_dir
        .join("objects")
        .join("sha256")
        .join(&object_hash[0..2])
        .join(&object_hash[2..4])
        .join(object_hash);
    let base = output_dir.join("rooms");
    let relative = pathdiff::diff_paths(&object, &base).unwrap_or(object);
    Ok(relative.to_string_lossy().into_owned())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::store::{ArchiveStore, EventRecord, MediaRefRecord, RoomRecord};
    use tempfile::tempdir;

    #[test]
    fn html_export_escapes_message_text() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.upsert_room(&RoomRecord {
            room_id: "!room:local".into(),
            name: Some("Room".into()),
            canonical_alias: None,
            avatar_mxc: None,
            joined_at: Some(0),
            last_prev_batch: None,
            backfill_token: None,
            backfill_done: true,
        })?;
        store.insert_event(&EventRecord {
            event_id: "$event".into(),
            room_id: "!room:local".into(),
            origin_server_ts: Some(0),
            sender: Some("@a:local".into()),
            event_type: "m.room.message".into(),
            state_key: None,
            msgtype: Some("m.text".into()),
            relates_to_event_id: None,
            relation_type: None,
            redacts_event_id: None,
            is_encrypted: false,
            is_redacted: false,
            body_text: Some("<script>alert(1)</script>".into()),
            formatted_body_html: None,
            raw_event: serde_json::json!({"event_id": "$event"}),
            decrypted_event: None,
            canonical_sha256: "hash".into(),
            received_at: 0,
            source_batch: "test".into(),
        })?;
        let out = dir.path().join("html");
        export_html(&store, dir.path(), &out)?;
        let html = fs::read_to_string(out.join("rooms").join("_room_local.html"))?;
        assert!(html.contains("&lt;script&gt;alert(1)&lt;/script&gt;"));
        Ok(())
    }

    #[test]
    fn html_export_hides_media_links_for_redacted_events() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.upsert_room(&RoomRecord {
            room_id: "!room:local".into(),
            name: Some("Room".into()),
            canonical_alias: None,
            avatar_mxc: None,
            joined_at: Some(0),
            last_prev_batch: None,
            backfill_token: None,
            backfill_done: true,
        })?;
        store.insert_event(&EventRecord {
            event_id: "$redacted".into(),
            room_id: "!room:local".into(),
            origin_server_ts: Some(0),
            sender: Some("@a:local".into()),
            event_type: "m.room.message".into(),
            state_key: None,
            msgtype: Some("m.image".into()),
            relates_to_event_id: None,
            relation_type: None,
            redacts_event_id: None,
            is_encrypted: false,
            is_redacted: true,
            body_text: None,
            formatted_body_html: None,
            raw_event: serde_json::json!({"event_id": "$redacted"}),
            decrypted_event: None,
            canonical_sha256: "hash".into(),
            received_at: 0,
            source_batch: "test".into(),
        })?;
        store.insert_media_ref(&MediaRefRecord {
            event_id: "$redacted".into(),
            field_path: "content.url".into(),
            mxc_uri: "mxc://server/redacted-media".into(),
            object_hash: Some(
                "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".into(),
            ),
            mimetype: None,
            original_filename: None,
            encrypted_file_json: None,
        })?;
        let out = dir.path().join("html");
        export_html(&store, dir.path(), &out)?;
        let html = fs::read_to_string(out.join("rooms").join("_room_local.html"))?;
        assert!(html.contains("redacted media hidden"));
        assert!(!html.contains("mxc://server/redacted-media"));
        Ok(())
    }

    #[test]
    fn html_export_embeds_readable_media_controls() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.upsert_room(&RoomRecord {
            room_id: "!room:local".into(),
            name: Some("Room".into()),
            canonical_alias: None,
            avatar_mxc: None,
            joined_at: Some(0),
            last_prev_batch: None,
            backfill_token: None,
            backfill_done: true,
        })?;
        store.insert_event(&EventRecord {
            event_id: "$image".into(),
            room_id: "!room:local".into(),
            origin_server_ts: Some(0),
            sender: Some("@a:local".into()),
            event_type: "m.room.message".into(),
            state_key: None,
            msgtype: Some("m.image".into()),
            relates_to_event_id: None,
            relation_type: None,
            redacts_event_id: None,
            is_encrypted: false,
            is_redacted: false,
            body_text: Some("photo".into()),
            formatted_body_html: None,
            raw_event: serde_json::json!({"event_id": "$image"}),
            decrypted_event: None,
            canonical_sha256: "hash".into(),
            received_at: 0,
            source_batch: "test".into(),
        })?;
        let object_hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
        store.insert_media_object(
            object_hash,
            12,
            Some("image/png"),
            Some("photo.png"),
            "objects/sha256/bb/bb/object",
        )?;
        store.insert_media_ref(&MediaRefRecord {
            event_id: "$image".into(),
            field_path: "content.url".into(),
            mxc_uri: "mxc://server/photo".into(),
            object_hash: Some(object_hash.into()),
            mimetype: None,
            original_filename: None,
            encrypted_file_json: None,
        })?;
        let out = dir.path().join("html");
        export_html(&store, dir.path(), &out)?;
        let html = fs::read_to_string(out.join("rooms").join("_room_local.html"))?;
        assert!(html.contains("<img loading=\"lazy\""));
        assert!(html.contains("alt=\"photo.png\""));
        assert!(html.contains("../objects/sha256/bb/bb/"));
        Ok(())
    }

    #[test]
    fn html_export_renders_beeper_sender_avatar() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.upsert_room(&RoomRecord {
            room_id: "!room:local".into(),
            name: Some("Room".into()),
            canonical_alias: None,
            avatar_mxc: None,
            joined_at: Some(0),
            last_prev_batch: None,
            backfill_token: None,
            backfill_done: true,
        })?;
        store.insert_event(&EventRecord {
            event_id: "$profile".into(),
            room_id: "!room:local".into(),
            origin_server_ts: Some(0),
            sender: Some("@bridge:local".into()),
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
            raw_event: serde_json::json!({
                "event_id": "$profile",
                "content": {
                    "com.beeper.per_message_profile": {
                        "displayname": "Alice Example",
                        "avatar_url": "mxc://server/avatar"
                    }
                }
            }),
            decrypted_event: None,
            canonical_sha256: "hash".into(),
            received_at: 0,
            source_batch: "test".into(),
        })?;
        let object_hash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
        store.insert_media_object(
            object_hash,
            12,
            Some("image/png"),
            Some("avatar.png"),
            "objects/sha256/cc/cc/object",
        )?;
        store.insert_media_ref(&MediaRefRecord {
            event_id: "$profile".into(),
            field_path: BEEPER_PROFILE_AVATAR_FIELD.into(),
            mxc_uri: "mxc://server/avatar".into(),
            object_hash: Some(object_hash.into()),
            mimetype: None,
            original_filename: None,
            encrypted_file_json: None,
        })?;
        let out = dir.path().join("html");
        export_html(&store, dir.path(), &out)?;
        let html = fs::read_to_string(out.join("rooms").join("_room_local.html"))?;
        assert!(html.contains("class=\"sender-avatar\""));
        assert!(html.contains("alt=\"Alice Example\""));
        assert!(html.contains(">Alice Example · "));
        assert!(!html.contains("mxc://server/avatar"));
        Ok(())
    }
}
