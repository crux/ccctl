#!/bin/bash
# Usage: write-formula.sh VERSION OUTPUT
# Writes a Homebrew formula for the release archives in dist/
# (<BINARY>-<VERSION>-<os>-<arch>.tar.gz, made by build-dist.sh).
# Uses BINARY, REPO, DESC, LICENSE, TAG_PREFIX, PLATFORMS from release.env.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/lib.sh
load_env release.env

VERSION=${1:?usage: write-formula.sh VERSION OUTPUT}
OUT=${2:?usage: write-formula.sh VERSION OUTPUT}
TAG="${TAG_PREFIX}v${VERSION}"
BASE="https://github.com/$REPO/releases/download/$TAG"

# Ruby class name: "my-tool" -> "MyTool".
CLASS=$(printf '%s' "$BINARY" | awk -F'-' '{for (i = 1; i <= NF; i++) printf "%s%s", toupper(substr($i, 1, 1)), substr($i, 2)}')

# block OS ARCH_DSL ARCH — one on_arm / on_intel block if the archive exists.
block() {
	local os=$1 dsl=$2 arch=$3 archive
	archive="$BINARY-$VERSION-$os-$arch.tar.gz"
	[ -f "dist/$archive" ] || return 0
	cat <<BLOCK
    $dsl do
      url "$BASE/$archive"
      sha256 "$(sha256_of "dist/$archive")"
    end
BLOCK
}

mac=$(block darwin on_arm arm64; block darwin on_intel amd64)
linux=$(block linux on_arm arm64; block linux on_intel amd64)
[ -n "$mac$linux" ] || die "no archives for $VERSION in dist/"

{
	echo "class $CLASS < Formula"
	echo "  desc \"$DESC\""
	echo "  homepage \"https://github.com/$REPO\""
	echo "  version \"$VERSION\""
	echo "  license \"$LICENSE\""
	echo
	if [ -n "$mac" ]; then
		echo "  on_macos do"
		echo "$mac"
		echo "  end"
	fi
	if [ -n "$linux" ]; then
		echo "  on_linux do"
		echo "$linux"
		echo "  end"
	else
		echo
		echo "  depends_on :macos"
	fi
	cat <<BODY

  def install
    bin.install "$BINARY"
    generate_completions_from_executable(bin/"$BINARY", "completion")
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/$BINARY --version")
  end
end
BODY
} > "$OUT"
echo "==> $OUT"
