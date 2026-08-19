#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  run_historical_rebuild.sh \
    --sessionctl FILE \
    --build-manifest FILE \
    --input-dir DIR \
    --output-root DIR \
    --label NAME \
    [--plan-only]

The script always creates a new run directory below --output-root. It never
changes input archives, uploads Drive objects, reseeds PostgreSQL, backfills
Token metrics, purges records, or deletes/replaces Drive objects.
EOF
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

manifest_value() {
  local file=$1
  local key=$2
  local count
  count=$(awk -F= -v wanted="$key" '$1 == wanted {count++} END {print count + 0}' "$file")
  if [[ "$count" -ne 1 ]]; then
    echo "Manifest must contain exactly one $key entry: $file" >&2
    return 1
  fi
  awk -F= -v wanted="$key" '$1 == wanted {sub(/^[^=]*=/, ""); print}' "$file"
}

sessionctl=""
build_manifest=""
input_dir=""
output_root=""
label=""
plan_only=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --sessionctl)
      sessionctl=${2:-}
      shift 2
      ;;
    --build-manifest)
      build_manifest=${2:-}
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

if [[ -z "$sessionctl" || -z "$build_manifest" || -z "$input_dir" || -z "$output_root" || -z "$label" ]]; then
  usage >&2
  exit 2
fi
if [[ ! -x "$sessionctl" ]]; then
  echo "sessionctl is not executable: $sessionctl" >&2
  exit 1
fi
if [[ ! -f "$build_manifest" ]]; then
  echo "Build manifest does not exist: $build_manifest" >&2
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
build_manifest_dir=$(cd "$(dirname "$build_manifest")" && pwd -P)
build_manifest="$build_manifest_dir/$(basename "$build_manifest")"

build_status=$(manifest_value "$build_manifest" status)
source_commit=$(manifest_value "$build_manifest" source_commit)
source_remote=$(manifest_value "$build_manifest" source_remote)
source_remote_refs=$(manifest_value "$build_manifest" source_remote_refs)
expected_binary_sha=$(manifest_value "$build_manifest" binary_sha256)
build_go_version=$(manifest_value "$build_manifest" go_version)
go_mod_sha=$(manifest_value "$build_manifest" go_mod_sha256)
go_sum_sha=$(manifest_value "$build_manifest" go_sum_sha256)
if [[ "$build_status" != "success" ]]; then
  echo "Build manifest is not successful: $build_manifest" >&2
  exit 1
fi
if [[ ! "$source_commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Build manifest source_commit is not a full lowercase Git SHA" >&2
  exit 1
fi
if [[ ! "$source_remote" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ || -z "$source_remote_refs" ]]; then
  echo "Build manifest remote identity fields are invalid" >&2
  exit 1
fi
if [[ ! "$expected_binary_sha" =~ ^[0-9a-f]{64}$ ]]; then
  echo "Build manifest binary_sha256 is invalid" >&2
  exit 1
fi
if [[ ! "$build_go_version" =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ || \
      ! "$go_mod_sha" =~ ^[0-9a-f]{64}$ || ! "$go_sum_sha" =~ ^[0-9a-f]{64}$ ]]; then
  echo "Build manifest Go identity fields are invalid" >&2
  exit 1
fi

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

binary_sha=$(sha256_file "$sessionctl")
if [[ "$binary_sha" != "$expected_binary_sha" ]]; then
  echo "sessionctl SHA-256 does not match its build manifest" >&2
  exit 1
fi
build_manifest_sha=$(sha256_file "$build_manifest")
run_stamp=$(date -u +%Y%m%dT%H%M%SZ)
run_name="session-delivery-rebuild-${run_stamp}-${label}-${binary_sha:0:8}"
run_dir="$output_root/$run_name"
artifact_dir="$run_dir/artifacts"
report_dir="$run_dir/reports"

printf 'archives=%s\ninput_kb=%s\nfree_kb=%s\nsource_commit=%s\nsessionctl_sha256=%s\nbuild_manifest_sha256=%s\nrun_dir=%s\n' \
  "$archive_count" "$input_kb" "$free_kb" "$source_commit" "$binary_sha" "$build_manifest_sha" "$run_dir"

if [[ "$plan_only" == true ]]; then
  exit 0
fi
if [[ -e "$run_dir" ]]; then
  echo "Refusing to reuse an existing run directory: $run_dir" >&2
  exit 1
fi
mkdir -m 0700 "$run_dir" "$artifact_dir" "$report_dir"
cp "$build_manifest" "$report_dir/BUILD_MANIFEST"
chmod 0600 "$report_dir/BUILD_MANIFEST"

finish_run() {
  rc=$?
  if [[ "$rc" -ne 0 && -d "$report_dir" ]]; then
    printf 'status=failed\nfinished_at_utc=%s\nexit_code=%s\n' \
      "$(date -u +%Y%m%dT%H%M%SZ)" "$rc" >"$report_dir/STATE"
    touch "$report_dir/FAILED"
  fi
}
trap finish_run EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cat >"$report_dir/RUN.txt" <<EOF
created_at_utc=$run_stamp
source_commit=$source_commit
source_remote=$source_remote
source_remote_refs=$source_remote_refs
sessionctl_sha256=$binary_sha
build_manifest_sha256=$build_manifest_sha
input_dir=$input_dir
artifact_dir=$artifact_dir
input_archives=$archive_count
input_kb=$input_kb
EOF
printf 'status=running\nstarted_at_utc=%s\n' "$run_stamp" >"$report_dir/STATE"

echo "Hashing immutable inputs..."
(
  cd "$input_dir"
  for archive in *.tar.zst; do
    printf '%s  %s\n' "$(sha256_file "$archive")" "$archive"
  done
) | LC_ALL=C sort -k2 >"$report_dir/INPUT_SHA256SUMS"

echo "Auditing immutable inputs..."
"$sessionctl" audit-fidelity -input-dir "$input_dir" | tee "$report_dir/input-audit.json"

echo "Rebuilding archives..."
"$sessionctl" rebuild-archives \
  -input-dir "$input_dir" \
  -output-dir "$artifact_dir" \
  -allow-rebuild | tee "$report_dir/rebuild.json"

rebuilt_count=$(find "$artifact_dir" -maxdepth 1 -type f -name '*.tar.zst' | wc -l | tr -d ' ')
if [[ "$rebuilt_count" -eq 0 ]]; then
  echo "Rebuild produced no archives" >&2
  exit 1
fi

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
    printf '%s  %s\n' "$(sha256_file "$archive")" "$archive"
  done
) | LC_ALL=C sort -k2 >"$report_dir/SHA256SUMS"

printf 'status=success\nfinished_at_utc=%s\narchives=%s\n' \
  "$(date -u +%Y%m%dT%H%M%SZ)" "$rebuilt_count" >"$report_dir/STATE"
touch "$report_dir/SUCCESS"
echo "Completed: $run_dir"
