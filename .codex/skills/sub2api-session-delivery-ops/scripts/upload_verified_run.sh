#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  upload_verified_run.sh \
    --sessionctl FILE \
    --run-dir DIR \
    --drive-dest REMOTE:PATH \
    [--rclone FILE]

Revalidates an existing historical rebuild run, requires an empty versioned
Drive destination, uploads immutable archives, and reads every object back to
verify SHA-256. It never deletes or replaces local or remote objects.
EOF
}

sessionctl=""
run_dir=""
drive_dest=""
rclone_bin="rclone"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --sessionctl)
      sessionctl=${2:-}
      shift 2
      ;;
    --run-dir)
      run_dir=${2:-}
      shift 2
      ;;
    --drive-dest)
      drive_dest=${2:-}
      shift 2
      ;;
    --rclone)
      rclone_bin=${2:-}
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$sessionctl" || -z "$run_dir" || -z "$drive_dest" ]]; then
  usage >&2
  exit 2
fi
if [[ ! -x "$sessionctl" ]]; then
  echo "sessionctl is not executable: $sessionctl" >&2
  exit 1
fi
if [[ ! -d "$run_dir/artifacts" || ! -d "$run_dir/reports" ]]; then
  echo "Run directory must contain artifacts/ and reports/: $run_dir" >&2
  exit 1
fi
case "$drive_dest" in
  *:*) ;;
  *)
    echo "Drive destination must include an rclone remote prefix" >&2
    exit 1
    ;;
esac
case "$drive_dest" in
  */session-delivery|*/session-delivery/*|*:session-delivery|*:session-delivery/*)
    echo "Refusing to upload into the live session-delivery directory" >&2
    exit 1
    ;;
esac
if ! command -v "$rclone_bin" >/dev/null 2>&1 && [[ ! -x "$rclone_bin" ]]; then
  echo "rclone is unavailable: $rclone_bin" >&2
  exit 1
fi

run_dir=$(cd "$run_dir" && pwd -P)
artifact_dir="$run_dir/artifacts"
report_dir="$run_dir/reports"
archive_count=$(find "$artifact_dir" -maxdepth 1 -type f -name '*.tar.zst' | wc -l | tr -d ' ')
if [[ "$archive_count" -eq 0 ]]; then
  echo "Run contains no rebuilt archives" >&2
  exit 1
fi

upload_stamp=$(date -u +%Y%m%dT%H%M%SZ)
current_sums="$report_dir/SHA256SUMS-${upload_stamp}"
remote_sums="$report_dir/REMOTE_SHA256SUMS-${upload_stamp}"

echo "Revalidating rebuilt archives before upload..."
for archive in "$artifact_dir"/*.tar.zst; do
  "$sessionctl" validate -archive "$archive" >/dev/null
done
"$sessionctl" audit-fidelity -input-dir "$artifact_dir" \
  >"$report_dir/pre-upload-audit-${upload_stamp}.json"

(
  cd "$artifact_dir"
  for archive in *.tar.zst; do
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "$archive"
    else
      shasum -a 256 "$archive"
    fi
  done
) | LC_ALL=C sort -k2 >"$current_sums"

if [[ ! -f "$report_dir/SHA256SUMS" ]]; then
  echo "Original local SHA256SUMS is missing" >&2
  exit 1
fi
diff -u "$report_dir/SHA256SUMS" "$current_sums"

remote_root="${drive_dest%%:*}:"
"$rclone_bin" about "$remote_root" >/dev/null
"$rclone_bin" mkdir "$drive_dest"
existing=$("$rclone_bin" lsf "$drive_dest" --max-depth 1)
if [[ -n "$existing" ]]; then
  echo "Refusing to upload into a non-empty Drive destination: $drive_dest" >&2
  exit 1
fi

echo "Uploading immutable archives to $drive_dest..."
"$rclone_bin" copy "$artifact_dir" "$drive_dest" \
  --include '*.tar.zst' \
  --immutable \
  --transfers 2 \
  --checkers 4 \
  --retries 3

: >"$remote_sums"
while read -r expected_sha filename; do
  filename=${filename#\*}
  if command -v sha256sum >/dev/null 2>&1; then
    remote_sha=$("$rclone_bin" cat "${drive_dest%/}/$filename" | sha256sum | awk '{print $1}')
  else
    remote_sha=$("$rclone_bin" cat "${drive_dest%/}/$filename" | shasum -a 256 | awk '{print $1}')
  fi
  if [[ "$remote_sha" != "$expected_sha" ]]; then
    echo "Drive read-back SHA mismatch for $filename" >&2
    exit 1
  fi
  printf '%s  %s\n' "$remote_sha" "$filename" >>"$remote_sums"
done <"$current_sums"
diff -u "$current_sums" "$remote_sums"

printf 'drive_dest=%s\narchives=%s\nremote_sha_file=%s\n' \
  "$drive_dest" "$archive_count" "$remote_sums"
