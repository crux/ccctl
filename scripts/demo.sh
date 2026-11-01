#!/usr/bin/env bash
# Usage: demo.sh [tape...]
# Renders the README demos (docs/demo/*.gif) with VHS in a Docker
# container. The container holds a made-up world (projects, sessions,
# running "claude" processes in tmux), so no real session appears.
# Default: every tape in docs/demo/env/.
set -euo pipefail
cd "$(dirname "$0")/.."

case "$(docker info --format '{{.Architecture}}')" in
aarch64 | arm64) goarch=arm64 ;;
*) goarch=amd64 ;;
esac

ctx=$(mktemp -d)
CGO_ENABLED=0 GOOS=linux GOARCH=$goarch go build -trimpath -o "$ctx/ccctl" ./cmd/ccctl
cp docs/demo/env/* "$ctx/"
docker build -q -t ccctl-demo "$ctx" >/dev/null

tapes=("$@")
if [ ${#tapes[@]} -eq 0 ]; then
	for t in docs/demo/env/*.tape; do tapes+=("$(basename "$t")"); done
fi
docker run --rm -v "$PWD/docs/demo:/out" ccctl-demo "${tapes[@]}"
ls -l docs/demo/*.gif
