#!/bin/bash
# Usage: release-notes.sh TAG [PATH...]
# Prints Markdown release notes: the commit subjects since the previous
# tag with the same prefix, merges left out. The first release lists no
# commits. Monorepos pass the component folder as PATH, so commits of
# other components stay out. Needs the full history (fetch-depth: 0).
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
load_env release.env

TAG=${1:?usage: release-notes.sh TAG [PATH...]}
shift

PREV=$(git describe --tags --abbrev=0 --match "${TAG_PREFIX}v[0-9]*" "${TAG}^" 2>/dev/null || true)

echo "## Changes"
echo
if [ -z "$PREV" ]; then
	echo "First release."
else
	echo "Commits since \`$PREV\`:"
	echo
	git log --no-merges --pretty=format:'- %s' "$PREV..$TAG" -- "$@"
	echo
fi
echo
echo "Built from commit \`$(git rev-parse --short "$TAG^{commit}")\`."
