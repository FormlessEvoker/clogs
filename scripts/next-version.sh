#!/usr/bin/env bash
set -euo pipefail

# Compute the next release tag from the current one and a bump kind. The
# release workflow reads the bump kind from the merged pull request's major,
# minor, or patch label, so the arithmetic lives here where it can be run and
# checked by hand rather than buried in workflow YAML.
#
# Usage: scripts/next-version.sh <current-tag> <major|minor|patch>
#
# An empty current tag is treated as v0.0.0, so the first release of a
# repository with no tags lands on v1.0.0, v0.1.0, or v0.0.1.

if (($# != 2)); then
  echo "usage: $0 <current-tag> <major|minor|patch>" >&2
  exit 2
fi

current=${1:-v0.0.0}
bump=$2

if [[ ! $current =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "current tag is not a release version: $current" >&2
  exit 1
fi

# Force base 10. A component such as 08 is an invalid octal literal, and a tag
# that carries one should not take the release pipeline down.
major=$((10#${BASH_REMATCH[1]}))
minor=$((10#${BASH_REMATCH[2]}))
patch=$((10#${BASH_REMATCH[3]}))

case $bump in
major)
  major=$((major + 1))
  minor=0
  patch=0
  ;;
minor)
  minor=$((minor + 1))
  patch=0
  ;;
patch)
  patch=$((patch + 1))
  ;;
*)
  echo "bump must be major, minor, or patch: $bump" >&2
  exit 1
  ;;
esac

printf 'v%d.%d.%d\n' "$major" "$minor" "$patch"
