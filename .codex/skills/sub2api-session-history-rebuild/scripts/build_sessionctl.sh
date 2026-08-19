#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  build_sessionctl.sh --repo PATH --git-ref REF --output FILE [--remote REMOTE]

Builds a native sessionctl from an immutable committed Git snapshot. The source
working tree is never modified and uncommitted files are never included. It
also writes FILE.manifest, which cryptographically binds the binary to the
source commit and Go module inputs.
EOF
}

repo=""
git_ref=""
output=""
remote="origin"

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
    --remote)
      remote=${2:-}
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
repo=$(cd "$repo" && pwd -P)
if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  echo "Not a Git repository: $repo" >&2
  exit 1
fi
commit=$(git -C "$repo" rev-parse --verify "${git_ref}^{commit}")
commit_time=$(git -C "$repo" show -s --format=%ct "$commit")
if [[ ! "$remote" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || \
    ! git -C "$repo" remote get-url "$remote" >/dev/null 2>&1; then
  echo "Configured Git remote is unavailable: $remote" >&2
  exit 1
fi
remote_refs=$(git -C "$repo" for-each-ref --format='%(refname)' --contains="$commit" "refs/remotes/$remote" \
  | sed 's#^refs/remotes/##' | LC_ALL=C sort | paste -sd, -)
if [[ -z "$remote_refs" ]]; then
  echo "Commit is not present in fetched $remote remote-tracking refs; fetch and push it before building" >&2
  exit 1
fi

output_parent=$(dirname "$output")
mkdir -p "$output_parent"
output_parent=$(cd "$output_parent" && pwd -P)
output="$output_parent/$(basename "$output")"
manifest="${output}.manifest"
if [[ -e "$output" || -e "$manifest" ]]; then
  echo "Refusing to overwrite existing output or manifest: $output" >&2
  exit 1
fi

tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
build_tmp=$(mktemp -d "$tmp_base/sub2api-sessionctl-build.XXXXXX")
cleanup() {
  case "$build_tmp" in
    "$tmp_base"/sub2api-sessionctl-build.*)
      rm -rf -- "$build_tmp"
      ;;
  esac
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$build_tmp/src"
git -C "$repo" archive --format=tar "$commit" | tar -xf - -C "$build_tmp/src"

required_go_version=$(awk '$1 == "go" {print $2; exit}' "$build_tmp/src/backend/go.mod")
go_version=$(GOTOOLCHAIN=local go env GOVERSION)
actual_go_version=${go_version#go}
oldest_go_version=$(printf '%s\n%s\n' "$required_go_version" "$actual_go_version" | sort -V | head -n 1)
if [[ -z "$required_go_version" || "$oldest_go_version" != "$required_go_version" ]]; then
  echo "Local Go toolchain $go_version does not satisfy go.mod requirement go$required_go_version" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  go_mod_sha=$(sha256sum "$build_tmp/src/backend/go.mod" | awk '{print $1}')
  go_sum_sha=$(sha256sum "$build_tmp/src/backend/go.sum" | awk '{print $1}')
else
  go_mod_sha=$(shasum -a 256 "$build_tmp/src/backend/go.mod" | awk '{print $1}')
  go_sum_sha=$(shasum -a 256 "$build_tmp/src/backend/go.sum" | awk '{print $1}')
fi

(
  cd "$build_tmp/src/backend"
  SOURCE_DATE_EPOCH="$commit_time" GOTOOLCHAIN=local CGO_ENABLED=0 \
    go build -mod=readonly -buildvcs=false -trimpath -o "$build_tmp/sessionctl" ./cmd/sessionctl
)
chmod 0700 "$build_tmp/sessionctl"

if command -v sha256sum >/dev/null 2>&1; then
  binary_sha=$(sha256sum "$build_tmp/sessionctl" | awk '{print $1}')
else
  binary_sha=$(shasum -a 256 "$build_tmp/sessionctl" | awk '{print $1}')
fi

cat >"$build_tmp/sessionctl.manifest" <<EOF
status=success
created_at_utc=$(date -u +%Y%m%dT%H%M%SZ)
source_commit=$commit
source_commit_time=$commit_time
source_remote=$remote
source_remote_refs=$remote_refs
binary_sha256=$binary_sha
go_version=$go_version
go_mod_sha256=$go_mod_sha
go_sum_sha256=$go_sum_sha
EOF
chmod 0600 "$build_tmp/sessionctl.manifest"

mv "$build_tmp/sessionctl" "$output"
mv "$build_tmp/sessionctl.manifest" "$manifest"

printf 'commit=%s\noutput=%s\nmanifest=%s\nsha256=%s\ngo_version=%s\n' \
  "$commit" "$output" "$manifest" "$binary_sha" "$go_version"
