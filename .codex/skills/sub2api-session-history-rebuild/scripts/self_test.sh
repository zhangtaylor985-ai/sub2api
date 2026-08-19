#!/usr/bin/env bash
set -euo pipefail

umask 077

skill_dir=$(cd "$(dirname "$0")/.." && pwd -P)
tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
test_tmp=$(mktemp -d "$tmp_base/sub2api-session-history-skill-test.XXXXXX")
cleanup() {
  case "$test_tmp" in
    "$tmp_base"/sub2api-session-history-skill-test.*)
      rm -rf -- "$test_tmp"
      ;;
  esac
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$test_tmp/input" "$test_tmp/output" "$test_tmp/remote"
printf 'archive-one\n' >"$test_tmp/input/2026-08-13T05.tar.zst"
printf 'archive-two\n' >"$test_tmp/input/2026-08-13T06.tar.zst"

cat >"$test_tmp/fake-sessionctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

command_name=${1:-}
shift || true
case "$command_name" in
  audit-fidelity)
    printf '{"violations":0}\n'
    ;;
  rebuild-archives)
    input_dir=""
    output_dir=""
    while [[ $# -gt 0 ]]; do
      case "$1" in
        -input-dir) input_dir=$2; shift 2 ;;
        -output-dir) output_dir=$2; shift 2 ;;
        -allow-rebuild) shift ;;
        *) shift ;;
      esac
    done
    cp "$input_dir"/*.tar.zst "$output_dir"/
    printf '{"rebuilt":2}\n'
    ;;
  validate)
    printf '{"valid":true}\n'
    ;;
  *)
    echo "unexpected fake sessionctl command: $command_name" >&2
    exit 2
    ;;
esac
EOF
chmod 0700 "$test_tmp/fake-sessionctl"

if command -v sha256sum >/dev/null 2>&1; then
  fake_binary_sha=$(sha256sum "$test_tmp/fake-sessionctl" | awk '{print $1}')
else
  fake_binary_sha=$(shasum -a 256 "$test_tmp/fake-sessionctl" | awk '{print $1}')
fi
cat >"$test_tmp/fake-sessionctl.manifest" <<EOF
status=success
created_at_utc=20260819T000000Z
source_commit=1111111111111111111111111111111111111111
source_commit_time=1787097600
source_remote=origin
source_remote_refs=origin/codex/session-delivery-v2-rollout
binary_sha256=$fake_binary_sha
go_version=go1.26.3
go_mod_sha256=2222222222222222222222222222222222222222222222222222222222222222
go_sum_sha256=3333333333333333333333333333333333333333333333333333333333333333
EOF
chmod 0600 "$test_tmp/fake-sessionctl.manifest"

sed 's/^binary_sha256=.*/binary_sha256=0000000000000000000000000000000000000000000000000000000000000000/' \
  "$test_tmp/fake-sessionctl.manifest" >"$test_tmp/tampered-build.manifest"
if "$skill_dir/scripts/run_historical_rebuild.sh" \
    --sessionctl "$test_tmp/fake-sessionctl" \
    --build-manifest "$test_tmp/tampered-build.manifest" \
    --input-dir "$test_tmp/input" \
    --output-root "$test_tmp/output" \
    --label tampered-build \
    --plan-only >/dev/null 2>&1; then
  echo "Rebuild unexpectedly accepted a mismatched build manifest" >&2
  exit 1
fi

for pass in pass1 pass2; do
  "$skill_dir/scripts/run_historical_rebuild.sh" \
    --sessionctl "$test_tmp/fake-sessionctl" \
    --build-manifest "$test_tmp/fake-sessionctl.manifest" \
    --input-dir "$test_tmp/input" \
    --output-root "$test_tmp/output" \
    --label "$pass" >/dev/null
done

pass1=$(find "$test_tmp/output" -maxdepth 1 -type d -name '*-pass1-*' -print)
pass2=$(find "$test_tmp/output" -maxdepth 1 -type d -name '*-pass2-*' -print)
"$skill_dir/scripts/verify_rebuild_idempotence.sh" \
  --first-run "$pass1" \
  --second-run "$pass2" \
  --attestation "$test_tmp/idempotence.attestation" >/dev/null

cat >"$test_tmp/fake-rclone" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

remote_path() {
  value=${1#*:}
  printf '%s/%s' "$FAKE_REMOTE_ROOT" "$value"
}

command_name=${1:-}
shift || true
case "$command_name" in
  about)
    exit 0
    ;;
  mkdir)
    mkdir -p "$(remote_path "$1")"
    ;;
  lsf)
    path=$(remote_path "$1")
    if [[ -d "$path" ]]; then
      for file in "$path"/*; do
        [[ -f "$file" ]] || continue
        basename "$file"
      done
    fi
    ;;
  copy)
    source_dir=$1
    destination=$(remote_path "$2")
    mkdir -p "$destination"
    cp "$source_dir"/*.tar.zst "$destination"/
    ;;
  cat)
    /bin/cat "$(remote_path "$1")"
    ;;
  *)
    echo "unexpected fake rclone command: $command_name" >&2
    exit 2
    ;;
esac
EOF
chmod 0700 "$test_tmp/fake-rclone"

if "$skill_dir/scripts/upload_verified_run.sh" \
    --sessionctl "$test_tmp/fake-sessionctl" \
    --run-dir "$pass1" \
    --idempotence-attestation "$test_tmp/idempotence.attestation" \
    --drive-dest 'other:Sub2API/session-delivery-rebuild-20260819-self-test-1111111' \
    --rclone "$test_tmp/fake-rclone" >/dev/null 2>&1; then
  echo "Upload unexpectedly accepted a non-project Drive remote" >&2
  exit 1
fi

sed 's/^output_manifest_sha256=.*/output_manifest_sha256=0000000000000000000000000000000000000000000000000000000000000000/' \
  "$test_tmp/idempotence.attestation" >"$test_tmp/tampered-idempotence.attestation"
if FAKE_REMOTE_ROOT="$test_tmp/remote" \
  "$skill_dir/scripts/upload_verified_run.sh" \
    --sessionctl "$test_tmp/fake-sessionctl" \
    --run-dir "$pass1" \
    --idempotence-attestation "$test_tmp/tampered-idempotence.attestation" \
    --drive-dest 'gdrive:Sub2API/session-delivery-rebuild-20260819-tampered-1111111' \
    --rclone "$test_tmp/fake-rclone" >/dev/null 2>&1; then
  echo "Upload unexpectedly accepted a tampered idempotence attestation" >&2
  exit 1
fi

grep -v '^go_sum_sha256=' "$test_tmp/idempotence.attestation" \
  >"$test_tmp/missing-go-identity.attestation"
if FAKE_REMOTE_ROOT="$test_tmp/remote" \
  "$skill_dir/scripts/upload_verified_run.sh" \
    --sessionctl "$test_tmp/fake-sessionctl" \
    --run-dir "$pass1" \
    --idempotence-attestation "$test_tmp/missing-go-identity.attestation" \
    --drive-dest 'gdrive:Sub2API/session-delivery-rebuild-20260819-missing-go-1111111' \
    --rclone "$test_tmp/fake-rclone" >/dev/null 2>&1; then
  echo "Upload unexpectedly accepted an attestation without Go module identity" >&2
  exit 1
fi

if FAKE_REMOTE_ROOT="$test_tmp/remote" \
  "$skill_dir/scripts/upload_verified_run.sh" \
    --sessionctl "$test_tmp/fake-sessionctl" \
    --run-dir "$pass1" \
    --idempotence-attestation "$test_tmp/idempotence.attestation" \
    --drive-dest 'gdrive:Sub2API/session-delivery-rebuild-20260819-wrong-abcdef0' \
    --rclone "$test_tmp/fake-rclone" >/dev/null 2>&1; then
  echo "Upload unexpectedly accepted a mismatched Drive commit suffix" >&2
  exit 1
fi

FAKE_REMOTE_ROOT="$test_tmp/remote" \
  "$skill_dir/scripts/upload_verified_run.sh" \
    --sessionctl "$test_tmp/fake-sessionctl" \
    --run-dir "$pass1" \
    --idempotence-attestation "$test_tmp/idempotence.attestation" \
    --drive-dest 'gdrive:Sub2API/session-delivery-rebuild-20260819-self-test-1111111' \
    --rclone "$test_tmp/fake-rclone" >/dev/null

remote_count=$(find "$test_tmp/remote" -type f -name '*.tar.zst' | wc -l | tr -d ' ')
if [[ "$remote_count" -ne 2 ]]; then
  echo "Expected two uploaded archives, got $remote_count" >&2
  exit 1
fi
printf 'history_skill_self_test=passed\n'
