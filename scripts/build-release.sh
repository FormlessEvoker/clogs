#!/usr/bin/env bash
set -euo pipefail

# Cross-compile the published release targets and package them.
#
# Usage: VERSION=v1.2.3 scripts/build-release.sh [os/arch ...]
#
# Output lands in bin/dist. That directory is ignored by git and skipped by
# the prohibited content gate, so build products never reach a commit or a
# scan. Each target produces one flat archive holding the binary, the README,
# and the license, and every archive is listed in checksums.txt.
#
# Set ARCHIVE=0 to compile without packaging. The pull request smoke check
# uses that to prove every target still builds without paying for archives.
#
# The SQLite driver is pure Go, so CGO_ENABLED=0 cross-compiles every target
# from a single runner. Adding a target is a line in the default list below.

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

version=${VERSION:-}
if [[ -z $version ]]; then
  echo "VERSION is required, for example VERSION=v1.2.3 $0" >&2
  exit 2
fi

archive=${ARCHIVE:-1}
app=clogs
dist=bin/dist
ldflags="-s -w -X github.com/FormlessEvoker/clogs/internal/cli.BuildVersion=$version"

if (($# > 0)); then
  targets=("$@")
else
  targets=(
    darwin/arm64
    darwin/amd64
    windows/amd64
    linux/amd64
    linux/arm64
  )
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

rm -rf "$dist"
mkdir -p "$dist"

for target in "${targets[@]}"; do
  goos=${target%%/*}
  goarch=${target##*/}
  if [[ -z $goos || -z $goarch || $goos == "$target" ]]; then
    echo "target must be written as os/arch: $target" >&2
    exit 1
  fi

  binary=$app
  [[ $goos == windows ]] && binary=$app.exe

  name=${app}_${version}_${goos}_${goarch}
  stage=$dist/$name
  mkdir -p "$stage"

  echo "building $goos/$goarch"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
    go build -trimpath -ldflags "$ldflags" -o "$stage/$binary" ./cmd/clogs

  if ((archive == 0)); then
    rm -rf "$stage"
    continue
  fi

  cp README.md LICENSE "$stage"

  if [[ $goos == windows ]]; then
    (cd "$stage" && zip -q "$repo_root/$dist/$name.zip" "$binary" README.md LICENSE)
  else
    tar -czf "$dist/$name.tar.gz" -C "$stage" "$binary" README.md LICENSE
  fi
  rm -rf "$stage"
done

if ((archive == 0)); then
  echo "all targets compiled"
  exit 0
fi

(
  cd "$dist"
  shopt -s nullglob
  archives=(*.tar.gz *.zip)
  if ((${#archives[@]} == 0)); then
    echo "no archives were produced" >&2
    exit 1
  fi
  sha256 "${archives[@]}" >checksums.txt
)

echo
cat "$dist/checksums.txt"
