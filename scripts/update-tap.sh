#!/bin/bash
# Usage: update-tap.sh KIND FILE MESSAGE
# Copies FILE into KIND/ (Formula or Casks) of TAP_REPO and pushes the
# change. Runs in CI with HOMEBREW_TAP_TOKEN (fine-grained, Contents read
# and write on the tap only). Works in a fresh temp folder; nothing is
# deleted. No change, no commit.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
load_env release.env

[ $# -eq 3 ] || die "usage: update-tap.sh KIND FILE MESSAGE"
KIND=$1 FILE=$2 MSG=$3
case "$KIND" in Formula|Casks) ;; *) die "KIND must be Formula or Casks" ;; esac
[ -f "$FILE" ] || die "missing $FILE"
: "${HOMEBREW_TAP_TOKEN:?HOMEBREW_TAP_TOKEN is not set}"
: "${TAP_REPO:?TAP_REPO is not set in release.env}"

work=$(mktemp -d)
git clone --quiet --depth 1 \
	"https://x-access-token:${HOMEBREW_TAP_TOKEN}@github.com/${TAP_REPO}.git" "$work/tap"

mkdir -p "$work/tap/$KIND"
cp "$FILE" "$work/tap/$KIND/"
cd "$work/tap"
git add "$KIND/$(basename "$FILE")"
if git diff --cached --quiet; then
	echo "==> tap: $KIND/$(basename "$FILE") unchanged"
	exit 0
fi
git -c user.name="github-actions[bot]" \
	-c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
	commit --quiet -m "$MSG"
git push --quiet
echo "==> tap: pushed $KIND/$(basename "$FILE") to $TAP_REPO"
