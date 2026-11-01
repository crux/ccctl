#!/bin/bash
# Usage: release.sh VERSION
# Cuts a release: checks the repo, creates the annotated tag
# <TAG_PREFIX>vVERSION and pushes it. CI builds and publishes the release.
# Dirk runs this through `make release VERSION=x.y.z`. Claude does not.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
load_env release.env

VERSION=${1:-}
[ -n "$VERSION" ] || die "usage: make release VERSION=x.y.z"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$ ]] \
	|| die "VERSION must look like 1.2.3 or 1.2.3-rc1 (no leading v)"
TAG="${TAG_PREFIX}v${VERSION}"
BRANCH=${RELEASE_BRANCH:-main}

[ "$(git rev-parse --abbrev-ref HEAD)" = "$BRANCH" ] \
	|| die "releases are cut from $BRANCH"
git diff-index --quiet HEAD -- || die "the working tree has changes; commit first"
git fetch --quiet origin "$BRANCH" --tags
[ -z "$(git rev-list "origin/$BRANCH..HEAD")" ] || die "unpushed commits on $BRANCH; push first"
[ -z "$(git rev-list "HEAD..origin/$BRANCH")" ] || die "$BRANCH is behind origin; pull first"
if git rev-parse --verify --quiet "refs/tags/$TAG" >/dev/null; then
	die "tag $TAG exists locally"
fi
if git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
	die "tag $TAG exists on origin"
fi

git tag -a "$TAG" -m "$TAG"
git push origin "$TAG"
echo "==> pushed $TAG; CI is building the release"
echo "    check: gh run list --workflow release.yml --limit 1"
