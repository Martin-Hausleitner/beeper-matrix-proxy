use std::fs;
use std::path::Path;

use anyhow::{bail, Context, Result};
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
    let snapshot_db = output_dir.join("archive.sqlite");
    let manifest_path = output_dir.join("manifest.json");
    for destination in [&snapshot_db, &manifest_path] {
        match fs::symlink_metadata(destination) {
            Ok(metadata) if !metadata.file_type().is_file() => {
                bail!(
                    "snapshot destination must be a regular file: {}",
                    destination.display()
                );
            }
            Ok(_) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
    }
    if fs::canonicalize(output_dir)?.join("archive.sqlite") == fs::canonicalize(store.db_path())? {
        bail!("snapshot destination must differ from the live archive database");
    }
    #[cfg(unix)]
    if let Ok(destination) = fs::metadata(&snapshot_db) {
        use std::os::unix::fs::MetadataExt;
        let source = fs::metadata(store.db_path())?;
        if source.dev() == destination.dev() && source.ino() == destination.ino() {
            bail!("snapshot destination aliases the live archive database");
        }
    }
    // VACUUM and manifest preparation leave the previous snapshot intact.
    // Publication uses two renames, not a transaction across the two files.
    let snapshot_file = tempfile::NamedTempFile::new_in(output_dir)?;
    let sql = format!(
        "VACUUM INTO '{}'",
        snapshot_file.path().to_string_lossy().replace('\'', "''")
    );
    let snapshot_conn = rusqlite::Connection::open(store.db_path())?;
    snapshot_conn.execute_batch(&sql)?;

    let snapshot_read = rusqlite::Connection::open_with_flags(
        snapshot_file.path(),
        rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY,
    )?;
    let mut statement = snapshot_read.prepare(
        "SELECT object_hash, size, storage_path FROM media_objects ORDER BY object_hash",
    )?;
    let objects = statement
        .query_map([], |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)))?
        .collect::<rusqlite::Result<Vec<(String, i64, String)>>>()?
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
    let manifest_bytes = serde_json::to_vec_pretty(&manifest)?;
    let mut manifest_file = tempfile::NamedTempFile::new_in(output_dir)?;
    use std::io::Write;
    manifest_file.write_all(&manifest_bytes)?;
    manifest_file.as_file().sync_all()?;
    snapshot_file.as_file().sync_all()?;
    snapshot_file.persist(&snapshot_db)?;
    manifest_file.persist(manifest_path)?;
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
        store.set_state("generation", "first")?;
        create_snapshot(&store, dir.path(), &out)?;
        assert!(out.join("archive.sqlite").exists());
        assert!(out.join("manifest.json").exists());
        store.set_state("generation", "second")?;
        create_snapshot(&store, dir.path(), &out)?;
        let copy = ArchiveStore::open(&out)?;
        assert_eq!(copy.get_state("generation")?.as_deref(), Some("second"));
        Ok(())
    }

    #[test]
    fn rejects_live_archive_as_snapshot_destination_without_changing_it() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.set_state("sentinel", "preserve-original")?;
        assert!(create_snapshot(&store, dir.path(), dir.path()).is_err());
        let reopened = ArchiveStore::open(dir.path())?;
        assert_eq!(
            reopened.get_state("sentinel")?.as_deref(),
            Some("preserve-original")
        );
        Ok(())
    }

    #[test]
    fn rejects_unpublishable_manifest_before_replacing_previous_database() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        let out = dir.path().join("snapshot");
        store.set_state("generation", "first")?;
        create_snapshot(&store, dir.path(), &out)?;
        let original = fs::read(out.join("archive.sqlite"))?;
        fs::remove_file(out.join("manifest.json"))?;
        fs::create_dir(out.join("manifest.json"))?;
        store.set_state("generation", "second")?;
        assert!(create_snapshot(&store, dir.path(), &out).is_err());
        assert_eq!(fs::read(out.join("archive.sqlite"))?, original);
        assert_eq!(store.get_state("generation")?.as_deref(), Some("second"));
        Ok(())
    }

    #[cfg(unix)]
    #[test]
    fn rejects_symlink_and_hardlink_aliases_to_the_live_database() -> Result<()> {
        let dir = tempdir()?;
        let store = ArchiveStore::open(dir.path())?;
        store.set_state("sentinel", "untouched")?;
        let out = dir.path().join("snapshot");
        fs::create_dir(&out)?;
        let destination = out.join("archive.sqlite");
        std::os::unix::fs::symlink(store.db_path(), &destination)?;
        assert!(create_snapshot(&store, dir.path(), &out).is_err());
        assert!(fs::symlink_metadata(&destination)?.file_type().is_symlink());
        fs::remove_file(&destination)?;
        fs::hard_link(store.db_path(), &destination)?;
        assert!(create_snapshot(&store, dir.path(), &out).is_err());
        assert_eq!(store.get_state("sentinel")?.as_deref(), Some("untouched"));
        Ok(())
    }
}
