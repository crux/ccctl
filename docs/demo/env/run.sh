#!/usr/bin/env bash
# Container entry point: build the made-up world, then render each tape
# given as an argument into /out.
set -euo pipefail
bash /demo/setup.sh
for tape in "$@"; do
	echo "==> $tape"
	vhs "/demo/$tape"
done
