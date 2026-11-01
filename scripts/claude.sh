#!/usr/bin/env bash
# Usage: claude.sh install|check
# The Claude Code commands in claude/commands/ are the source; Claude's
# commands folder holds copies. CLAUDE_COMMANDS_DIR names that folder
# (default ~/.claude/commands).
#   install   copy each command over its installed copy
#   check     fail when an installed copy is missing or differs
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
dest="${CLAUDE_COMMANDS_DIR:-$HOME/.claude/commands}"

case "${1:-}" in
install)
	mkdir -p "$dest"
	for f in "$repo"/claude/commands/*.md; do
		cp "$f" "$dest/"
		echo "install: /$(basename "$f" .md)"
	done
	;;
check)
	failed=0
	for f in "$repo"/claude/commands/*.md; do
		name=$(basename "$f")
		if [ ! -f "$dest/$name" ]; then
			echo "check: /${name%.md} is not installed — run make claude-install" >&2
			failed=1
		elif ! cmp -s "$f" "$dest/$name"; then
			echo "check: /${name%.md} differs from claude/commands/$name — run make claude-install, or bring the change back first" >&2
			failed=1
		else
			echo "check: /${name%.md} is current"
		fi
	done
	exit "$failed"
	;;
*)
	echo "usage: $0 install|check" >&2
	exit 2
	;;
esac
