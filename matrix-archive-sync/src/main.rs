mod archive;
mod cli;
mod exporter;
mod matrix;
mod media;
mod snapshot;
mod store;

use anyhow::Result;
use clap::Parser;

use crate::archive::Archiver;
use crate::cli::{Cli, Command};
use crate::exporter::{export_html, export_jsonl};
use crate::matrix::MatrixClient;
use crate::snapshot::create_snapshot;
use crate::store::ArchiveStore;

#[tokio::main]
async fn main() -> Result<()> {
    let cli = Cli::parse();
    let store = ArchiveStore::open(&cli.archive_dir)?;

    match cli.command {
        Command::Sync(args) => {
            let client = MatrixClient::from_options(args.matrix.clone(), args.insecure_tls)?;
            let archiver = Archiver::new(client, store, cli.archive_dir);
            archiver.sync(args).await
        }
        Command::Backfill(args) => {
            let client = MatrixClient::from_options(args.matrix.clone(), args.insecure_tls)?;
            let archiver = Archiver::new(client, store, cli.archive_dir);
            archiver.backfill(args).await
        }
        Command::ExportHtml(args) => export_html(&store, &cli.archive_dir, &args.output_dir),
        Command::ExportJsonl(args) => export_jsonl(&store, &args.output),
        Command::Snapshot(args) => create_snapshot(&store, &cli.archive_dir, &args.output_dir),
    }
}
