use std::fs;
use std::path::Path;

use anyhow::{Context, Result};
use chrono::Utc;
use serde::Serialize;

use crate::store::ArchiveStore;

#[derive(Serialize)]
struct SnapshotManifest {
    created_at: String,
    database: String,
    media_objects: Vec<SnapshotObject>,
}

#[derive(Serialize)]
struct SnapshotObject {
    object_hash: String,
    size: i64,
    storage_path: String,
}

pub fn create_snapshot(store: &ArchiveStore, archive_dir: &Path, output_dir: &Path) -> Result<()> {
    fs::create_dir_all(output_dir)
        .with_context(|| format!("create snapshot directory {}", output_dir.display()))?;
    store.checkpoint()?;
    let snapshot_db = output_dir.join("archive.sqlite");
    if snapshot_db.exists() {
        fs::remove_file(&snapshot_db)?;
    }
    let legacy_snapshot_db = output_dir.join("archive.sqlite.snapshot");
    if legacy_snapshot_db.exists() {
        fs::remove_file(&legacy_snapshot_db)?;
    }
    let sql = format!(
        "VACUUM INTO '{}'",
        snapshot_db.to_string_lossy().replace('\'', "''")
    );
    let snapshot_conn = rusqlite::Connection::open(store.db_path())?;
    snapshot_conn.execute_batch(&sql)?;

    let objects = store
        .media_objects()?
        .into_iter()
        .map(|(object_hash, size, storage_path)| SnapshotObject {
            object_hash,
            size,
            storage_path,
        })
        .collect();
    let manifest = SnapshotManifest {
        created_at: Utc::now().to_rfc3339(),
        database: snapshot_db
            .strip_prefix(archive_dir)
            .unwrap_or(&snapshot_db)
            .to_string_lossy()
            .into_owned(),
        media_objects: objects,
    };
    fs::write(
        output_dir.join("manifest.json"),
        serde_json::to_vec_pretty(&manifest)?,
    )?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn creates_snapshot_database_and_manifest() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        let out = dir.path().join("snapshot");
        create_snapshot(&store, dir.path(), &out)?;
        assert!(out.join("archive.sqlite").exists());
        assert!(out.join("manifest.json").exists());
        Ok(())
    }
}
