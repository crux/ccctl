#!/bin/bash
# Usage: tap.sh VERSION
# Writes the Homebrew formula (TAP_KIND=Formula) or cask (TAP_KIND=Casks)
# for VERSION and pushes it to TAP_REPO. Runs in CI after publish, with
# HOMEBREW_TAP_TOKEN set.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
load_env release.env

VERSION=${1:?usage: tap.sh VERSION}
out=$(mktemp -d)

case "$TAP_KIND" in
Formula)
	file="$out/$BINARY.rb"
	bash scripts/write-formula.sh "$VERSION" "$file"
	;;
Casks)
	dmg="dist/$APP_NAME-$VERSION.dmg"
	[ -f "$dmg" ] || die "missing $dmg"
	url="https://github.com/$REPO/releases/download/${TAG_PREFIX}v$VERSION/$APP_NAME-$VERSION.dmg"
	file="$out/$BINARY.rb"
	bash scripts/write-cask.sh "$BINARY" "$VERSION" "$(sha256_of "$dmg")" "$url" "$APP_NAME.app" "$file"
	;;
*)
	die "TAP_KIND must be Formula or Casks (release.env)"
	;;
esac

bash scripts/update-tap.sh "$TAP_KIND" "$file" "$BINARY $VERSION"
