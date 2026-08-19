#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  verify_rebuild_idempotence.sh \
    --first-run DIR \
    --second-run DIR \
    --attestation FILE

Requires two successful historical rebuild runs created from the same immutable
input and sessionctl. Recomputes every artifact SHA-256 and proves that object
names and bytes are identical. Writes a new machine-verifiable attestation that
binds both run names, the source commit, build manifest, input manifest, output
manifest, sessionctl binary, and archive count. Existing files are not replaced.
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
    echo "Expected exactly one $key entry in $file" >&2
    return 1
  fi
  awk -F= -v wanted="$key" '$1 == wanted {sub(/^[^=]*=/, ""); print}' "$file"
}

first_run=""
second_run=""
attestation=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --first-run)
      first_run=${2:-}
      shift 2
      ;;
    --second-run)
      second_run=${2:-}
      shift 2
      ;;
    --attestation)
      attestation=${2:-}
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

if [[ -z "$first_run" || -z "$second_run" || -z "$attestation" ]]; then
  usage >&2
  exit 2
fi
for run in "$first_run" "$second_run"; do
  if [[ ! -d "$run/artifacts" || ! -d "$run/reports" || ! -f "$run/reports/SUCCESS" ]]; then
    echo "Run is not a completed SUCCESS run: $run" >&2
    exit 1
  fi
  if [[ ! -f "$run/reports/SHA256SUMS" || ! -f "$run/reports/INPUT_SHA256SUMS" || \
        ! -f "$run/reports/RUN.txt" || ! -f "$run/reports/BUILD_MANIFEST" ]]; then
    echo "Run is missing identity or SHA manifests: $run" >&2
    exit 1
  fi
done

first_run=$(cd "$first_run" && pwd -P)
second_run=$(cd "$second_run" && pwd -P)
if [[ "$first_run" == "$second_run" ]]; then
  echo "Two distinct run directories are required" >&2
  exit 1
fi
attestation_parent=$(dirname "$attestation")
if [[ ! -d "$attestation_parent" ]]; then
  echo "Attestation parent directory does not exist: $attestation_parent" >&2
  exit 1
fi
attestation_parent=$(cd "$attestation_parent" && pwd -P)
attestation="$attestation_parent/$(basename "$attestation")"
if [[ -e "$attestation" ]]; then
  echo "Refusing to overwrite existing attestation: $attestation" >&2
  exit 1
fi

first_binary=$(manifest_value "$first_run/reports/RUN.txt" sessionctl_sha256)
second_binary=$(manifest_value "$second_run/reports/RUN.txt" sessionctl_sha256)
if [[ ! "$first_binary" =~ ^[0-9a-f]{64}$ || "$first_binary" != "$second_binary" ]]; then
  echo "Runs were not built with the same sessionctl SHA-256" >&2
  exit 1
fi
first_commit=$(manifest_value "$first_run/reports/RUN.txt" source_commit)
second_commit=$(manifest_value "$second_run/reports/RUN.txt" source_commit)
if [[ ! "$first_commit" =~ ^[0-9a-f]{40}$ || "$first_commit" != "$second_commit" ]]; then
  echo "Runs were not built from the same full Git commit" >&2
  exit 1
fi
first_build_manifest_sha=$(manifest_value "$first_run/reports/RUN.txt" build_manifest_sha256)
second_build_manifest_sha=$(manifest_value "$second_run/reports/RUN.txt" build_manifest_sha256)
if [[ ! "$first_build_manifest_sha" =~ ^[0-9a-f]{64}$ || "$first_build_manifest_sha" != "$second_build_manifest_sha" ]]; then
  echo "Runs do not reference the same build manifest SHA-256" >&2
  exit 1
fi
if [[ "$(sha256_file "$first_run/reports/BUILD_MANIFEST")" != "$first_build_manifest_sha" || \
      "$(sha256_file "$second_run/reports/BUILD_MANIFEST")" != "$second_build_manifest_sha" ]]; then
  echo "Copied build manifest SHA-256 does not match RUN.txt" >&2
  exit 1
fi
diff -u "$first_run/reports/BUILD_MANIFEST" "$second_run/reports/BUILD_MANIFEST"
manifest_go_version=$(manifest_value "$first_run/reports/BUILD_MANIFEST" go_version)
manifest_go_mod_sha=$(manifest_value "$first_run/reports/BUILD_MANIFEST" go_mod_sha256)
manifest_go_sum_sha=$(manifest_value "$first_run/reports/BUILD_MANIFEST" go_sum_sha256)
manifest_source_remote=$(manifest_value "$first_run/reports/BUILD_MANIFEST" source_remote)
manifest_source_remote_refs=$(manifest_value "$first_run/reports/BUILD_MANIFEST" source_remote_refs)
if [[ "$(manifest_value "$first_run/reports/BUILD_MANIFEST" status)" != "success" || \
      "$(manifest_value "$first_run/reports/BUILD_MANIFEST" source_commit)" != "$first_commit" || \
      "$(manifest_value "$first_run/reports/BUILD_MANIFEST" binary_sha256)" != "$first_binary" || \
      ! "$manifest_source_remote" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ || -z "$manifest_source_remote_refs" || \
      ! "$manifest_go_version" =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ || \
      ! "$manifest_go_mod_sha" =~ ^[0-9a-f]{64}$ || ! "$manifest_go_sum_sha" =~ ^[0-9a-f]{64}$ ]]; then
  echo "Build manifest identity does not match run identity" >&2
  exit 1
fi
diff -u "$first_run/reports/INPUT_SHA256SUMS" "$second_run/reports/INPUT_SHA256SUMS"
input_manifest_sha=$(sha256_file "$first_run/reports/INPUT_SHA256SUMS")

tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
compare_tmp=$(mktemp -d "$tmp_base/sub2api-session-idempotence.XXXXXX")
cleanup() {
  case "$compare_tmp" in
    "$tmp_base"/sub2api-session-idempotence.*)
      rm -rf -- "$compare_tmp"
      ;;
  esac
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

compute_sums() {
  local run=$1
  local destination=$2
  (
    cd "$run/artifacts"
    for archive in *.tar.zst; do
      printf '%s  %s\n' "$(sha256_file "$archive")" "$archive"
    done
  ) | LC_ALL=C sort -k2 >"$destination"
}

compute_sums "$first_run" "$compare_tmp/first"
compute_sums "$second_run" "$compare_tmp/second"
diff -u "$first_run/reports/SHA256SUMS" "$compare_tmp/first"
diff -u "$second_run/reports/SHA256SUMS" "$compare_tmp/second"
diff -u "$compare_tmp/first" "$compare_tmp/second"
output_manifest_sha=$(sha256_file "$compare_tmp/first")

archive_count=0
while read -r _ filename; do
  filename=${filename#\*}
  cmp "$first_run/artifacts/$filename" "$second_run/artifacts/$filename"
  archive_count=$((archive_count + 1))
done <"$compare_tmp/first"

if [[ "$archive_count" -eq 0 ]]; then
  echo "No archives were compared" >&2
  exit 1
fi
cat >"$compare_tmp/ATTESTATION" <<EOF
status=identical
created_at_utc=$(date -u +%Y%m%dT%H%M%SZ)
first_run_name=$(basename "$first_run")
second_run_name=$(basename "$second_run")
source_commit=$first_commit
sessionctl_sha256=$first_binary
build_manifest_sha256=$first_build_manifest_sha
go_version=$manifest_go_version
go_mod_sha256=$manifest_go_mod_sha
go_sum_sha256=$manifest_go_sum_sha
input_manifest_sha256=$input_manifest_sha
output_manifest_sha256=$output_manifest_sha
archives=$archive_count
EOF
chmod 0600 "$compare_tmp/ATTESTATION"
mv "$compare_tmp/ATTESTATION" "$attestation"

printf 'status=identical\narchives=%s\nsessionctl_sha256=%s\nattestation=%s\n' \
  "$archive_count" "$first_binary" "$attestation"
