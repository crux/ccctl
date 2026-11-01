# Shared helpers for the release scripts. Source it from a script:
#   . "$(dirname "$0")/lib.sh"
# Works with bash on macOS and Linux.

# load_env FILE — read KEY=VALUE lines into shell variables without
# executing the file. Skips empty lines and comments. Values keep their
# spaces; quotes are not removed (do not use them in release.env).
load_env() {
	local file=${1:-release.env} key value
	[ -f "$file" ] || { echo "missing $file" >&2; return 1; }
	while IFS='=' read -r key value || [ -n "$key" ]; do
		case "$key" in ''|\#*) continue ;; esac
		key=$(printf '%s' "$key" | tr -d '[:space:]')
		printf -v "$key" '%s' "$value"
	done < "$file"
}

# sha256_file FILE — write FILE.sha256 next to FILE, in the format that
# `shasum -a 256 -c` and `sha256sum -c` accept (hash, two spaces, name).
sha256_file() {
	local dir base hash
	dir=$(dirname "$1")
	base=$(basename "$1")
	if command -v shasum >/dev/null 2>&1; then
		hash=$(cd "$dir" && shasum -a 256 "$base" | cut -d' ' -f1)
	else
		hash=$(cd "$dir" && sha256sum "$base" | cut -d' ' -f1)
	fi
	printf '%s  %s\n' "$hash" "$base" > "$1.sha256"
}

# sha256_of FILE — print the hash only.
sha256_of() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		sha256sum "$1" | cut -d' ' -f1
	fi
}

die() { echo "error: $*" >&2; exit 1; }
