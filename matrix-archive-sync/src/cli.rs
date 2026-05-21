use std::path::PathBuf;

use clap::{Args, Parser, Subcommand};

#[derive(Debug, Parser)]
#[command(version, about)]
pub struct Cli {
    #[arg(long, env = "MATRIX_ARCHIVE_DIR", default_value = "matrix-archive")]
    pub archive_dir: PathBuf,

    #[command(subcommand)]
    pub command: Command,
}

#[derive(Debug, Clone, Args)]
pub struct MatrixOptions {
    #[arg(long, env = "MATRIX_HOMESERVER_URL")]
    pub homeserver: String,

    #[arg(long, env = "MATRIX_ACCESS_TOKEN")]
    pub access_token: String,
}

#[derive(Debug, Subcommand)]
pub enum Command {
    /// Run one or more Matrix /sync passes and persist reachable live events.
    Sync(SyncArgs),
    /// Retroactively fetch room history through /rooms/{roomId}/messages.
    Backfill(BackfillArgs),
    /// Render an offline static HTML archive.
    ExportHtml(ExportHtmlArgs),
    /// Export archived events as JSONL.
    ExportJsonl(ExportJsonlArgs),
    /// Create a consistent DB snapshot and media manifest for restic/borg/kopia.
    Snapshot(SnapshotArgs),
}

#[derive(Debug, Clone, Args)]
pub struct SyncArgs {
    #[command(flatten)]
    pub matrix: MatrixOptions,

    #[arg(long, default_value_t = false)]
    pub follow: bool,

    #[arg(long, default_value_t = 30_000)]
    pub timeout_ms: u64,

    #[arg(long, default_value_t = 50)]
    pub timeline_limit: u32,

    #[arg(long, default_value_t = 1)]
    pub passes: u32,

    #[arg(long, default_value_t = false)]
    pub download_media: bool,

    #[arg(long, default_value_t = false)]
    pub insecure_tls: bool,
}

#[derive(Debug, Clone, Args)]
pub struct BackfillArgs {
    #[command(flatten)]
    pub matrix: MatrixOptions,

    #[arg(long, default_value_t = 100)]
    pub batch_limit: u32,

    #[arg(long, default_value_t = 0)]
    pub max_batches_per_room: u32,

    #[arg(long, default_value_t = 2)]
    pub room_limit: usize,

    #[arg(long, default_value_t = false)]
    pub download_media: bool,

    #[arg(long, default_value_t = false)]
    pub insecure_tls: bool,
}

#[derive(Debug, Clone, Args)]
pub struct ExportHtmlArgs {
    #[arg(long, default_value = "matrix-archive/html")]
    pub output_dir: PathBuf,
}

#[derive(Debug, Clone, Args)]
pub struct ExportJsonlArgs {
    #[arg(long, default_value = "matrix-archive/events.jsonl")]
    pub output: PathBuf,
}

#[derive(Debug, Clone, Args)]
pub struct SnapshotArgs {
    #[arg(long, default_value = "matrix-archive/snapshot")]
    pub output_dir: PathBuf,
}
