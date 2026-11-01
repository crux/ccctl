#!/bin/bash
# Usage: build-dist.sh VERSION
# Cross-compiles MAIN for every os/arch in PLATFORMS (release.env) and
# writes one archive per platform into dist/:
#   <BINARY>-<VERSION>-<os>-<arch>.tar.gz   (.zip for windows)
# Each archive holds a top folder with the binary, LICENSE and README.md.
# Every archive gets a .sha256 file next to it. Pure Go (CGO off); CGO
# platforms need their own build step (release-go, "CGO").
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
load_env release.env

VERSION=${1:?usage: build-dist.sh VERSION}
mkdir -p dist
stage=$(mktemp -d)
# macOS tar would add AppleDouble (._*) files for extended attributes.
export COPYFILE_DISABLE=1

IFS=',' read -r -a platforms <<< "$PLATFORMS"
for p in "${platforms[@]}"; do
	os=${p%/*}
	arch=${p#*/}
	name="$BINARY-$VERSION-$os-$arch"
	exe=$BINARY
	[ "$os" = windows ] && exe="$BINARY.exe"

	mkdir -p "$stage/$name"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X main.version=$VERSION" \
		-o "$stage/$name/$exe" "$MAIN"
	for doc in LICENSE README.md; do
		[ -f "$doc" ] && cp "$doc" "$stage/$name/"
	done

	if [ "$os" = windows ]; then
		archive="dist/$name.zip"
		(cd "$stage" && zip -qr - "$name") > "$archive"
	else
		archive="dist/$name.tar.gz"
		tar -czf "$archive" -C "$stage" "$name"
	fi
	sha256_file "$archive"
	echo "==> $archive"
done
