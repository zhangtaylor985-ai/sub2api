#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  build_sessionctl.sh --repo PATH --git-ref REF --output FILE

Builds a native sessionctl from an immutable committed Git snapshot. The source
working tree is never modified and uncommitted files are never included.
EOF
}

repo=""
git_ref=""
output=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      repo=${2:-}
      shift 2
      ;;
    --git-ref)
      git_ref=${2:-}
      shift 2
      ;;
    --output)
      output=${2:-}
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

if [[ -z "$repo" || -z "$git_ref" || -z "$output" ]]; then
  usage >&2
  exit 2
fi
if [[ ! -d "$repo" ]]; then
  echo "Repository directory does not exist: $repo" >&2
  exit 1
fi
if ! repo=$(cd "$repo" && pwd -P); then
  echo "Cannot resolve repository: $repo" >&2
  exit 1
fi
if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  echo "Not a Git repository: $repo" >&2
  exit 1
fi
commit=$(git -C "$repo" rev-parse --verify "${git_ref}^{commit}")

if [[ -e "$output" ]]; then
  echo "Refusing to overwrite existing output: $output" >&2
  exit 1
fi
output_parent=$(dirname "$output")
mkdir -p "$output_parent"
output_parent=$(cd "$output_parent" && pwd -P)
output="$output_parent/$(basename "$output")"

tmp_base=${TMPDIR:-/tmp}
build_tmp=$(mktemp -d "$tmp_base/sub2api-sessionctl-build.XXXXXX")
cleanup() {
  case "$build_tmp" in
    "$tmp_base"/sub2api-sessionctl-build.*)
      rm -rf -- "$build_tmp"
      ;;
  esac
}
trap cleanup EXIT INT TERM

mkdir -p "$build_tmp/src"
git -C "$repo" archive --format=tar "$commit" | tar -xf - -C "$build_tmp/src"

(
  cd "$build_tmp/src/backend"
  CGO_ENABLED=0 go build -trimpath -o "$build_tmp/sessionctl" ./cmd/sessionctl
)
chmod 0700 "$build_tmp/sessionctl"
mv "$build_tmp/sessionctl" "$output"

if command -v sha256sum >/dev/null 2>&1; then
  binary_sha=$(sha256sum "$output" | awk '{print $1}')
else
  binary_sha=$(shasum -a 256 "$output" | awk '{print $1}')
fi

printf 'commit=%s\noutput=%s\nsha256=%s\n' "$commit" "$output" "$binary_sha"
