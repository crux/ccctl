# Demo renderer for the README: VHS plus tmux and zsh, ccctl (linux
# build) and a stand-in `claude`. scripts/demo.sh builds the context.
FROM ghcr.io/charmbracelet/vhs:latest

RUN apt-get update \
 && apt-get install -y --no-install-recommends tmux zsh procps \
 && rm -rf /var/lib/apt/lists/*

COPY ccctl /usr/local/bin/ccctl
COPY claude /usr/local/bin/claude
COPY . /demo/
RUN chmod +x /usr/local/bin/ccctl /usr/local/bin/claude /demo/*.sh

ENTRYPOINT ["/bin/bash", "/demo/run.sh"]
