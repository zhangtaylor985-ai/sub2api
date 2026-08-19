#!/usr/bin/env bash
set -euo pipefail

umask 077

usage() {
  cat <<'EOF'
Usage:
  build_session_release.sh \
    --repo PATH \
    --git-ref REF \
    --output-root DIR \
    [--label NAME] \
    [--remote REMOTE] \
    [--pnpm-version VERSION]

Builds a production Session release from an immutable committed Git snapshot:
  - Linux ARM64 Sub2API app with the embed build tag
  - Linux ARM64 sessionctl
  - Linux AMD64 sessionctl
  - Linux AMD64 sessiond

The script builds the frontend from the same Git snapshot, creates a new release
directory, writes SHA256SUMS and MANIFEST.txt, and never overwrites an existing
release. It does not connect to or modify production.
EOF
}

repo=""
git_ref=""
output_root=""
label="session"
remote="origin"
pnpm_version="9.15.9"

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
    --output-root)
      output_root=${2:-}
      shift 2
      ;;
    --label)
      label=${2:-}
      shift 2
      ;;
    --remote)
      remote=${2:-}
      shift 2
      ;;
    --pnpm-version)
      pnpm_version=${2:-}
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

if [[ -z "$repo" || -z "$git_ref" || -z "$output_root" ]]; then
  usage >&2
  exit 2
fi
if [[ ! "$label" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]]; then
  echo "Label must contain only letters, numbers, dot, underscore, or hyphen" >&2
  exit 1
fi
if [[ ! "$pnpm_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "pnpm version must be an exact semantic version" >&2
  exit 1
fi
if [[ ! -d "$repo" || ! -d "$output_root" ]]; then
  echo "Repository and output root must already exist" >&2
  exit 1
fi

repo=$(cd "$repo" && pwd -P)
output_root=$(cd "$output_root" && pwd -P)
if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
  echo "Not a Git repository: $repo" >&2
  exit 1
fi
commit=$(git -C "$repo" rev-parse --verify "${git_ref}^{commit}")
short_commit=${commit:0:10}
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
stamp=$(date -u +%Y%m%dT%H%M%SZ)
release_name="session-release-${stamp}-${label}-${short_commit}"
release_dir="$output_root/$release_name"
if [[ -e "$release_dir" ]]; then
  echo "Refusing to overwrite existing release: $release_dir" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required" >&2
  exit 1
fi
if ! command -v corepack >/dev/null 2>&1; then
  echo "corepack is required to run the pinned pnpm frontend builder" >&2
  exit 1
fi
pnpm_cmd=(corepack "pnpm@$pnpm_version")

tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
source_tmp=$(mktemp -d "$tmp_base/sub2api-session-release-source.XXXXXX")
release_stage=$(mktemp -d "$output_root/.sub2api-session-release.XXXXXX")
cleanup() {
  case "$source_tmp" in
    "$tmp_base"/sub2api-session-release-source.*)
      rm -rf -- "$source_tmp"
      ;;
  esac
  if [[ -n "${release_stage:-}" ]]; then
    case "$release_stage" in
      "$output_root"/.sub2api-session-release.*)
        rm -rf -- "$release_stage"
        ;;
    esac
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$source_tmp/src"
git -C "$repo" archive --format=tar "$commit" | tar -xf - -C "$source_tmp/src"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

required_go_version=$(awk '$1 == "go" {print $2; exit}' "$source_tmp/src/backend/go.mod")
go_version=$(GOTOOLCHAIN=local go env GOVERSION)
actual_go_version=${go_version#go}
oldest_go_version=$(printf '%s\n%s\n' "$required_go_version" "$actual_go_version" | sort -V | head -n 1)
if [[ -z "$required_go_version" || "$oldest_go_version" != "$required_go_version" ]]; then
  echo "Local Go toolchain $go_version does not satisfy go.mod requirement go$required_go_version" >&2
  exit 1
fi
go_mod_sha=$(sha256_file "$source_tmp/src/backend/go.mod")
go_sum_sha=$(sha256_file "$source_tmp/src/backend/go.sum")

echo "Building frontend from commit $commit..."
frontend_log="$release_stage/frontend-build.log"
if ! (
  cd "$source_tmp/src/frontend"
  "${pnpm_cmd[@]}" install --frozen-lockfile --prefer-offline
  "${pnpm_cmd[@]}" run build
) >"$frontend_log" 2>&1; then
  echo "Frontend build failed; last 100 log lines:" >&2
  tail -n 100 "$frontend_log" >&2
  exit 1
fi
if [[ ! -s "$source_tmp/src/backend/internal/web/dist/index.html" ]]; then
  echo "Frontend build did not produce backend/internal/web/dist/index.html" >&2
  exit 1
fi
echo "Frontend build completed; full log will be packaged as frontend-build.log"

build_go() {
  local goos=$1
  local goarch=$2
  local output=$3
  local package=$4
  shift 4
  (
    cd "$source_tmp/src/backend"
    SOURCE_DATE_EPOCH="$commit_time" GOTOOLCHAIN=local \
      CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -mod=readonly -buildvcs=false -trimpath "$@" -o "$output" "$package"
  )
  chmod 0755 "$output"
}

echo "Building Linux production binaries..."
build_go linux arm64 "$release_stage/sub2api-linux-arm64" ./cmd/server -tags=embed
build_go linux arm64 "$release_stage/sessionctl-linux-arm64" ./cmd/sessionctl
build_go linux amd64 "$release_stage/sessionctl-linux-amd64" ./cmd/sessionctl
build_go linux amd64 "$release_stage/sessiond-linux-amd64" ./cmd/sessiond

if ! go version -m "$release_stage/sub2api-linux-arm64" | grep -Eq 'build[[:space:]]+-tags=embed'; then
  echo "Built app does not report the required embed build tag" >&2
  exit 1
fi

(
  cd "$release_stage"
  for binary in \
    sessionctl-linux-amd64 \
    sessionctl-linux-arm64 \
    sessiond-linux-amd64 \
    sub2api-linux-arm64; do
    printf '%s  %s\n' "$(sha256_file "$binary")" "$binary"
  done
) >"$release_stage/SHA256SUMS"

frontend_sha=$(sha256_file "$source_tmp/src/backend/internal/web/dist/index.html")
cat >"$release_stage/MANIFEST.txt" <<EOF
status=success
created_at_utc=$stamp
source_commit=$commit
source_commit_time=$commit_time
source_remote=$remote
source_remote_refs=$remote_refs
release_name=$release_name
frontend_index_sha256=$frontend_sha
app_build_tags=embed
go_version=$go_version
go_mod_sha256=$go_mod_sha
go_sum_sha256=$go_sum_sha
node_version=$(node --version 2>/dev/null || printf 'unknown')
pnpm_version=$("${pnpm_cmd[@]}" --version)
EOF
touch "$release_stage/SUCCESS"

mv "$release_stage" "$release_dir"
release_stage=""

printf 'commit=%s\nrelease_dir=%s\nsha256sums=%s\n' \
  "$commit" "$release_dir" "$release_dir/SHA256SUMS"
