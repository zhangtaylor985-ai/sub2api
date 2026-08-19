#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  upload_verified_run.sh \
    --sessionctl FILE \
    --run-dir DIR \
    --idempotence-attestation FILE \
    --drive-dest REMOTE:PATH \
    [--rclone FILE]

Revalidates a successful historical rebuild run and a two-run idempotence
attestation, requires an empty versioned project Drive destination, uploads
immutable archives, and reads every object back to verify SHA-256. It never
deletes or replaces local or remote objects.
EOF
}

sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
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

sessionctl=""
run_dir=""
attestation=""
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
    --idempotence-attestation)
      attestation=${2:-}
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

if [[ -z "$sessionctl" || -z "$run_dir" || -z "$attestation" || -z "$drive_dest" ]]; then
  usage >&2
  exit 2
fi
if [[ ! -x "$sessionctl" ]]; then
  echo "sessionctl is not executable: $sessionctl" >&2
  exit 1
fi
if [[ ! -d "$run_dir/artifacts" || ! -d "$run_dir/reports" || ! -f "$run_dir/reports/SUCCESS" ]]; then
  echo "Run directory must be a completed SUCCESS run: $run_dir" >&2
  exit 1
fi
if [[ ! -f "$attestation" ]]; then
  echo "Idempotence attestation does not exist: $attestation" >&2
  exit 1
fi
if ! grep -qx 'status=success' "$run_dir/reports/STATE"; then
  echo "Run STATE is not success: $run_dir" >&2
  exit 1
fi
drive_dest=${drive_dest%/}
if [[ ! "$drive_dest" =~ ^gdrive:Sub2API/session-delivery-rebuild-[0-9]{8}-[A-Za-z0-9][A-Za-z0-9._-]*-[0-9a-f]{7,40}$ ]]; then
  echo "Drive destination must match gdrive:Sub2API/session-delivery-rebuild-YYYYMMDD-VERSION-COMMIT" >&2
  exit 1
fi
if ! command -v "$rclone_bin" >/dev/null 2>&1 && [[ ! -x "$rclone_bin" ]]; then
  echo "rclone is unavailable: $rclone_bin" >&2
  exit 1
fi

run_dir=$(cd "$run_dir" && pwd -P)
attestation_dir=$(cd "$(dirname "$attestation")" && pwd -P)
attestation="$attestation_dir/$(basename "$attestation")"
artifact_dir="$run_dir/artifacts"
report_dir="$run_dir/reports"
archive_count=$(find "$artifact_dir" -maxdepth 1 -type f -name '*.tar.zst' | wc -l | tr -d ' ')
if [[ "$archive_count" -eq 0 ]]; then
  echo "Run contains no rebuilt archives" >&2
  exit 1
fi

for required_report in RUN.txt BUILD_MANIFEST INPUT_SHA256SUMS SHA256SUMS; do
  if [[ ! -f "$report_dir/$required_report" ]]; then
    echo "Run is missing required report: $required_report" >&2
    exit 1
  fi
done

attestation_status=$(manifest_value "$attestation" status)
first_run_name=$(manifest_value "$attestation" first_run_name)
second_run_name=$(manifest_value "$attestation" second_run_name)
attested_commit=$(manifest_value "$attestation" source_commit)
attested_binary=$(manifest_value "$attestation" sessionctl_sha256)
attested_build_manifest=$(manifest_value "$attestation" build_manifest_sha256)
attested_go_version=$(manifest_value "$attestation" go_version)
attested_go_mod_sha=$(manifest_value "$attestation" go_mod_sha256)
attested_go_sum_sha=$(manifest_value "$attestation" go_sum_sha256)
attested_input_manifest=$(manifest_value "$attestation" input_manifest_sha256)
attested_output_manifest=$(manifest_value "$attestation" output_manifest_sha256)
attested_archives=$(manifest_value "$attestation" archives)
run_name=$(basename "$run_dir")

if [[ "$attestation_status" != "identical" ]]; then
  echo "Idempotence attestation status is not identical" >&2
  exit 1
fi
drive_commit_suffix=${drive_dest##*-}
case "$attested_commit" in
  "$drive_commit_suffix"*) ;;
  *)
    echo "Drive destination commit suffix does not match the attested source commit" >&2
    exit 1
    ;;
esac
if [[ "$run_name" != "$first_run_name" && "$run_name" != "$second_run_name" ]]; then
  echo "Selected run is not one of the two attested rebuilds" >&2
  exit 1
fi
if [[ ! "$attested_commit" =~ ^[0-9a-f]{40}$ || ! "$attested_binary" =~ ^[0-9a-f]{64}$ || \
      ! "$attested_build_manifest" =~ ^[0-9a-f]{64}$ || ! "$attested_input_manifest" =~ ^[0-9a-f]{64}$ || \
      ! "$attested_output_manifest" =~ ^[0-9a-f]{64}$ || ! "$attested_archives" =~ ^[1-9][0-9]*$ ]]; then
  echo "Idempotence attestation contains invalid identity fields" >&2
  exit 1
fi
if [[ "$(sha256_file "$sessionctl")" != "$attested_binary" ]]; then
  echo "Selected sessionctl does not match the attested binary" >&2
  exit 1
fi
if [[ "$(manifest_value "$report_dir/RUN.txt" source_commit)" != "$attested_commit" || \
      "$(manifest_value "$report_dir/RUN.txt" sessionctl_sha256)" != "$attested_binary" || \
      "$(manifest_value "$report_dir/RUN.txt" build_manifest_sha256)" != "$attested_build_manifest" ]]; then
  echo "Run identity does not match the idempotence attestation" >&2
  exit 1
fi
manifest_go_version=$(manifest_value "$report_dir/BUILD_MANIFEST" go_version)
manifest_go_mod_sha=$(manifest_value "$report_dir/BUILD_MANIFEST" go_mod_sha256)
manifest_go_sum_sha=$(manifest_value "$report_dir/BUILD_MANIFEST" go_sum_sha256)
manifest_source_remote=$(manifest_value "$report_dir/BUILD_MANIFEST" source_remote)
manifest_source_remote_refs=$(manifest_value "$report_dir/BUILD_MANIFEST" source_remote_refs)
if [[ "$(sha256_file "$report_dir/BUILD_MANIFEST")" != "$attested_build_manifest" || \
      "$(manifest_value "$report_dir/BUILD_MANIFEST" status)" != "success" || \
      "$(manifest_value "$report_dir/BUILD_MANIFEST" source_commit)" != "$attested_commit" || \
      "$(manifest_value "$report_dir/BUILD_MANIFEST" binary_sha256)" != "$attested_binary" || \
      "$manifest_go_version" != "$attested_go_version" || "$manifest_go_mod_sha" != "$attested_go_mod_sha" || \
      "$manifest_go_sum_sha" != "$attested_go_sum_sha" || \
      ! "$manifest_source_remote" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ || -z "$manifest_source_remote_refs" || \
      ! "$manifest_go_version" =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ || \
      ! "$manifest_go_mod_sha" =~ ^[0-9a-f]{64}$ || ! "$manifest_go_sum_sha" =~ ^[0-9a-f]{64}$ ]]; then
  echo "Build manifest does not match the attested identity" >&2
  exit 1
fi
if [[ "$(sha256_file "$report_dir/INPUT_SHA256SUMS")" != "$attested_input_manifest" ]]; then
  echo "Input SHA manifest does not match the idempotence attestation" >&2
  exit 1
fi
if [[ "$archive_count" -ne "$attested_archives" ]]; then
  echo "Artifact count does not match the idempotence attestation" >&2
  exit 1
fi

upload_stamp=$(date -u +%Y%m%dT%H%M%SZ)
current_sums="$report_dir/SHA256SUMS-${upload_stamp}"
remote_sums="$report_dir/REMOTE_SHA256SUMS-${upload_stamp}"
expected_files="$report_dir/EXPECTED_FILES-${upload_stamp}"
remote_files="$report_dir/REMOTE_FILES-${upload_stamp}"

echo "Revalidating rebuilt archives before upload..."
for archive in "$artifact_dir"/*.tar.zst; do
  "$sessionctl" validate -archive "$archive" >/dev/null
done
"$sessionctl" audit-fidelity -input-dir "$artifact_dir" \
  >"$report_dir/pre-upload-audit-${upload_stamp}.json"

(
  cd "$artifact_dir"
  for archive in *.tar.zst; do
    printf '%s  %s\n' "$(sha256_file "$archive")" "$archive"
  done
) | LC_ALL=C sort -k2 >"$current_sums"

if [[ ! -f "$report_dir/SHA256SUMS" ]]; then
  echo "Original local SHA256SUMS is missing" >&2
  exit 1
fi
diff -u "$report_dir/SHA256SUMS" "$current_sums"
if [[ "$(sha256_file "$current_sums")" != "$attested_output_manifest" ]]; then
  echo "Current output SHA manifest does not match the idempotence attestation" >&2
  exit 1
fi
cp "$attestation" "$report_dir/IDEMPOTENCE_ATTESTATION-${upload_stamp}"
chmod 0600 "$report_dir/IDEMPOTENCE_ATTESTATION-${upload_stamp}"
awk '{print $2}' "$current_sums" | sed 's/^\*//' | LC_ALL=C sort >"$expected_files"

remote_root="gdrive:"
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

"$rclone_bin" lsf "$drive_dest" --max-depth 1 --files-only \
  | LC_ALL=C sort >"$remote_files"
diff -u "$expected_files" "$remote_files"

: >"$remote_sums"
while read -r expected_sha filename; do
  filename=${filename#\*}
  remote_sha=$("$rclone_bin" cat "${drive_dest%/}/$filename" | sha256_stream)
  if [[ "$remote_sha" != "$expected_sha" ]]; then
    echo "Drive read-back SHA mismatch for $filename" >&2
    exit 1
  fi
  printf '%s  %s\n' "$remote_sha" "$filename" >>"$remote_sums"
done <"$current_sums"
diff -u "$current_sums" "$remote_sums"

printf 'drive_dest=%s\narchives=%s\nremote_sha_file=%s\n' \
  "$drive_dest" "$archive_count" "$remote_sums"
