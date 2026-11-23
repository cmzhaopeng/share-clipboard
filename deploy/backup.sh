#!/usr/bin/env bash
# Daily snapshot of shared-clipboard data.
#  - clipboard.db: consistent online copy via SQLite's backup API (safe while the app runs)
#  - files/: rsync with --link-dest, so unchanged attachments are hard links (no extra space)
# Snapshots older than KEEP_DAYS are removed. Run as the service user (see shared-clipboard-backup.service).
set -euo pipefail

DATA_DIR=${DATA_DIR:-/opt/shared-clipboard/data}
BACKUP_ROOT=${BACKUP_ROOT:-/var/backups/shared-clipboard}
KEEP_DAYS=${KEEP_DAYS:-14}

stamp=$(date +%Y%m%d-%H%M%S)
dest="$BACKUP_ROOT/$stamp"
latest="$BACKUP_ROOT/latest"

mkdir -p "$dest"
chmod 700 "$BACKUP_ROOT" "$dest"

sqlite3 "$DATA_DIR/clipboard.db" ".backup '$dest/clipboard.db'"
if [[ "$(sqlite3 "$dest/clipboard.db" 'PRAGMA integrity_check;')" != "ok" ]]; then
  echo "integrity check failed for $dest/clipboard.db" >&2
  exit 1
fi

link_dest=()
if [[ -d "$latest/files" ]]; then
  link_dest=(--link-dest="$(readlink -f "$latest")/files")
fi
rsync -a --delete "${link_dest[@]}" "$DATA_DIR/files/" "$dest/files/"

ln -sfn "$stamp" "$latest"
find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -name '20*' -mtime +"$KEEP_DAYS" -exec rm -rf {} +

echo "backup ok: $dest ($(du -sh "$dest" | cut -f1) apparent, $(ls "$BACKUP_ROOT" | grep -c '^20') snapshot(s) kept)"
