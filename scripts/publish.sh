#!/bin/bash
# Usage: publish.sh VERSION
# Creates the GitHub release for <TAG_PREFIX>vVERSION with every file in
# dist/ (top level only) as an asset. Notes come from release-notes.sh.
# A version with "-" (1.2.3-rc1) is marked as pre-release. Runs in CI
# with GH_TOKEN set; the tag must already exist on origin.
# When the release exists already (a re-run after a later step failed),
# the files are uploaded again and the notes stay as they are. So a
# re-run of the whole workflow reaches the later steps again.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
load_env release.env

VERSION=${1:?usage: publish.sh VERSION}
TAG="${TAG_PREFIX}v${VERSION}"

assets=()
while IFS= read -r f; do assets+=("$f"); done < <(find dist -maxdepth 1 -type f ! -name '.*' | sort)
[ ${#assets[@]} -gt 0 ] || die "dist/ has no files to publish"

if gh release view "$TAG" >/dev/null 2>&1; then
	gh release upload "$TAG" --clobber "${assets[@]}"
	echo "==> $TAG exists; uploaded ${#assets[@]} files again"
	exit 0
fi

notes=$(mktemp)
bash scripts/release-notes.sh "$TAG" > "$notes"

flags=()
case "$VERSION" in *-*) flags+=(--prerelease) ;; esac

gh release create "$TAG" \
	--verify-tag \
	--title "$NAME $VERSION" \
	--notes-file "$notes" \
	"${flags[@]+"${flags[@]}"}" \
	"${assets[@]}"
echo "==> released $TAG with ${#assets[@]} files"
