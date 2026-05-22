use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use serde_json::Value;
use sha2::{Digest, Sha256};

use crate::matrix::DownloadedMedia;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MxcReference {
    pub field_path: String,
    pub mxc_uri: String,
    pub encrypted_file_json: Option<Value>,
}

pub struct MediaStore {
    root: PathBuf,
}

impl MediaStore {
    pub fn new(archive_dir: &Path) -> Self {
        Self {
            root: archive_dir.join("objects").join("sha256"),
        }
    }

    pub fn store(&self, media: &DownloadedMedia) -> Result<StoredMedia> {
        let hash = sha256_hex(&media.bytes);
        let relative_path = object_relative_path(&hash);
        let absolute_path = self.root.join(&relative_path);
        if !absolute_path.exists() {
            if let Some(parent) = absolute_path.parent() {
                fs::create_dir_all(parent).with_context(|| {
                    format!("create media object directory {}", parent.display())
                })?;
            }
            let mut file = fs::File::create(&absolute_path)
                .with_context(|| format!("create media object {}", absolute_path.display()))?;
            file.write_all(&media.bytes)
                .with_context(|| format!("write media object {}", absolute_path.display()))?;
        }
        Ok(StoredMedia {
            hash,
            size: media.bytes.len() as u64,
            relative_path: format!("objects/sha256/{}", relative_path.display()),
        })
    }
}

pub struct StoredMedia {
    pub hash: String,
    pub size: u64,
    pub relative_path: String,
}

pub fn extract_mxc_references(event: &Value) -> Vec<MxcReference> {
    let mut refs = Vec::new();
    let content = event.get("content").unwrap_or(&Value::Null);
    push_mxc(&mut refs, content, "content.url");
    push_mxc(&mut refs, content, "content.thumbnail_url");
    push_nested_mxc(
        &mut refs,
        content,
        &["com.beeper.per_message_profile", "avatar_url"],
        "content.com.beeper.per_message_profile.avatar_url",
    );
    push_encrypted_mxc(&mut refs, content, "content.file");
    push_encrypted_mxc(&mut refs, content, "content.thumbnail_file");
    refs
}

pub fn sha256_hex(bytes: &[u8]) -> String {
    let mut hasher = Sha256::new();
    hasher.update(bytes);
    hex::encode(hasher.finalize())
}

fn push_mxc(refs: &mut Vec<MxcReference>, content: &Value, field_path: &str) {
    let key = field_path.rsplit('.').next().unwrap_or(field_path);
    if let Some(mxc_uri) = content.get(key).and_then(Value::as_str) {
        if mxc_uri.starts_with("mxc://") {
            refs.push(MxcReference {
                field_path: field_path.to_owned(),
                mxc_uri: mxc_uri.to_owned(),
                encrypted_file_json: None,
            });
        }
    }
}

fn push_nested_mxc(refs: &mut Vec<MxcReference>, content: &Value, keys: &[&str], field_path: &str) {
    let mut value = content;
    for key in keys {
        let Some(next) = value.get(*key) else {
            return;
        };
        value = next;
    }
    if let Some(mxc_uri) = value.as_str() {
        if mxc_uri.starts_with("mxc://") {
            refs.push(MxcReference {
                field_path: field_path.to_owned(),
                mxc_uri: mxc_uri.to_owned(),
                encrypted_file_json: None,
            });
        }
    }
}

fn push_encrypted_mxc(refs: &mut Vec<MxcReference>, content: &Value, field_path: &str) {
    let key = field_path.rsplit('.').next().unwrap_or(field_path);
    let Some(file) = content.get(key) else {
        return;
    };
    if let Some(mxc_uri) = file.get("url").and_then(Value::as_str) {
        if mxc_uri.starts_with("mxc://") {
            refs.push(MxcReference {
                field_path: field_path.to_owned(),
                mxc_uri: mxc_uri.to_owned(),
                encrypted_file_json: Some(file.clone()),
            });
        }
    }
}

fn object_relative_path(hash: &str) -> PathBuf {
    PathBuf::from(&hash[0..2]).join(&hash[2..4]).join(hash)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn extracts_plain_and_encrypted_mxc_references() {
        let event = serde_json::json!({
            "content": {
                "url": "mxc://server/plain",
                "com.beeper.per_message_profile": {
                    "avatar_url": "mxc://server/avatar"
                },
                "thumbnail_file": {"url": "mxc://server/thumb", "key": {"k": "secret"}}
            }
        });
        let refs = extract_mxc_references(&event);
        assert_eq!(refs.len(), 3);
        assert_eq!(refs[0].field_path, "content.url");
        assert_eq!(
            refs[1].field_path,
            "content.com.beeper.per_message_profile.avatar_url"
        );
        assert_eq!(refs[2].field_path, "content.thumbnail_file");
        assert!(refs[2].encrypted_file_json.is_some());
    }

    #[test]
    fn stores_media_by_sha256() -> Result<()> {
        let dir = tempdir()?;
        let store = MediaStore::new(dir.path());
        let media = DownloadedMedia {
            bytes: b"hello".to_vec(),
            mimetype: Some("text/plain".into()),
        };
        let stored = store.store(&media)?;
        assert_eq!(
            stored.hash,
            "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
        );
        assert!(dir.path().join(&stored.relative_path).exists());
        Ok(())
    }
}
