#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  run_historical_rebuild.sh \
    --sessionctl FILE \
    --input-dir DIR \
    --output-root DIR \
    --label NAME \
    [--source-ref REF] \
    [--plan-only]

The script always creates a new run directory below --output-root. It never
changes the input archives, uploads Drive objects, reseeds PostgreSQL, backfills
Token metrics, purges records, or deletes/replaces Drive objects.
EOF
}

sessionctl=""
input_dir=""
output_root=""
label=""
source_ref="unknown"
plan_only=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --sessionctl)
      sessionctl=${2:-}
      shift 2
      ;;
    --input-dir)
      input_dir=${2:-}
      shift 2
      ;;
    --output-root)
      output_root=${2:-}
      shift 2
      ;;
    --label)
      label=${2:-}
      shift 2
      ;;
    --source-ref)
      source_ref=${2:-}
      shift 2
      ;;
    --plan-only)
      plan_only=true
      shift
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

if [[ -z "$sessionctl" || -z "$input_dir" || -z "$output_root" || -z "$label" ]]; then
  usage >&2
  exit 2
fi
if [[ ! -x "$sessionctl" ]]; then
  echo "sessionctl is not executable: $sessionctl" >&2
  exit 1
fi
if [[ ! -d "$input_dir" || ! -d "$output_root" ]]; then
  echo "Input directory and output root must already exist" >&2
  exit 1
fi
if [[ ! "$label" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]]; then
  echo "Label must contain only letters, numbers, dot, underscore, or hyphen" >&2
  exit 1
fi
input_dir=$(cd "$input_dir" && pwd -P)
output_root=$(cd "$output_root" && pwd -P)
sessionctl_dir=$(cd "$(dirname "$sessionctl")" && pwd -P)
sessionctl="$sessionctl_dir/$(basename "$sessionctl")"

case "$output_root/" in
  "$input_dir/"*)
    echo "Output root must not be inside the immutable input directory" >&2
    exit 1
    ;;
esac
if [[ "$input_dir" == "$output_root" ]]; then
  echo "Input directory and output root must differ" >&2
  exit 1
fi

archive_count=$(find "$input_dir" -maxdepth 1 -type f -name '*.tar.zst' | wc -l | tr -d ' ')
if [[ "$archive_count" -eq 0 ]]; then
  echo "Input directory contains no .tar.zst archives" >&2
  exit 1
fi
input_kb=$(du -sk "$input_dir" | awk '{print $1}')
free_kb=$(df -Pk "$output_root" | awk 'NR==2 {print $4}')
required_kb=$((input_kb * 2))
if [[ "$free_kb" -lt "$required_kb" ]]; then
  echo "Insufficient free space: free_kb=$free_kb required_kb=$required_kb" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  binary_sha=$(sha256sum "$sessionctl" | awk '{print $1}')
else
  binary_sha=$(shasum -a 256 "$sessionctl" | awk '{print $1}')
fi
run_stamp=$(date -u +%Y%m%dT%H%M%SZ)
run_name="session-delivery-rebuild-${run_stamp}-${label}-${binary_sha:0:8}"
run_dir="$output_root/$run_name"
artifact_dir="$run_dir/artifacts"
report_dir="$run_dir/reports"

printf 'archives=%s\ninput_kb=%s\nfree_kb=%s\nsessionctl_sha256=%s\nrun_dir=%s\n' \
  "$archive_count" "$input_kb" "$free_kb" "$binary_sha" "$run_dir"

if [[ "$plan_only" == true ]]; then
  exit 0
fi
if [[ -e "$run_dir" ]]; then
  echo "Refusing to reuse an existing run directory: $run_dir" >&2
  exit 1
fi
mkdir -m 0700 "$run_dir" "$artifact_dir" "$report_dir"

printf 'created_at_utc=%s\nsource_ref=%s\nsessionctl_sha256=%s\ninput_dir=%s\nartifact_dir=%s\n' \
  "$run_stamp" "$source_ref" "$binary_sha" "$input_dir" "$artifact_dir" \
  >"$report_dir/RUN.txt"

echo "Auditing immutable inputs..."
"$sessionctl" audit-fidelity -input-dir "$input_dir" | tee "$report_dir/input-audit.json"

echo "Rebuilding archives..."
"$sessionctl" rebuild-archives \
  -input-dir "$input_dir" \
  -output-dir "$artifact_dir" \
  -allow-rebuild | tee "$report_dir/rebuild.json"

echo "Validating rebuilt archives..."
for archive in "$artifact_dir"/*.tar.zst; do
  base=$(basename "$archive" .tar.zst)
  "$sessionctl" validate -archive "$archive" >"$report_dir/validate-${base}.json"
done

echo "Auditing rebuilt chronological sequence..."
"$sessionctl" audit-fidelity -input-dir "$artifact_dir" | tee "$report_dir/output-audit.json"

(
  cd "$artifact_dir"
  for archive in *.tar.zst; do
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "$archive"
    else
      shasum -a 256 "$archive"
    fi
  done
) | LC_ALL=C sort -k2 >"$report_dir/SHA256SUMS"

echo "Completed: $run_dir"
