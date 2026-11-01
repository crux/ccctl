# ccctl

ccctl is a command line tool and a TUI for the Claude Code sessions on
your Mac. It lists, searches, archives and moves sessions. It also
renames a project directory and takes its Claude Code history along.

ccctl is an unofficial tool and is not affiliated with Anthropic. It
reads files that Claude Code writes under `~/.claude/`. Anthropic does
not document these files, so a Claude Code update can change them.
ccctl is tested with Claude Code 2.1.316 on macOS 27.

## Commands

| Command | What it does |
|---|---|
| `ccctl`, `ccctl status [dir]` | overview of the local Claude Code state |
| `ccctl ps` | running Claude Code processes and their sessions |
| `ccctl projects` | project folders under `~/.claude/projects/`, newest first |
| `ccctl sessions [dir]` | sessions of a project directory, newest first |
| `ccctl find <pattern>` | search the text of all conversations |
| `ccctl focus <session>` | bring the terminal of a running session to the front (iTerm2, Terminal.app, tmux) |
| `ccctl move <session> <dir>` | move one session to another project |
| `ccctl move <dir> <new-path>` | rename a directory and take its Claude Code state along |
| `ccctl archive <session>` | pack a session into a `.zip`; `ccctl restore` unpacks it |
| `ccctl ui` | TUI for projects, sessions and running processes |

`ccctl <command> --help` shows the details.

## What ccctl changes

Most commands only read. These commands change files:

- `move <session> <dir>` moves the files of one session from one project
  folder to another.
- `move <dir> <new-path>` renames the directory and its project folders.
  It copies the directory's settings in `~/.claude.json` and changes the
  project path in `~/.claude/history.jsonl`. It edits both files under
  Claude Code's own file lock. It refuses while Claude Code runs inside
  the directory. `--dry-run` shows the plan and changes nothing.
- `archive` packs a session into `<session>.zip`. It checks the archive
  before it removes the original files. `restore` unpacks it again.

## Install

### Homebrew

```sh
brew install crux/tap/ccctl
```

Homebrew also installs the shell completion files.

### Go

```sh
go install github.com/crux/ccctl/cmd/ccctl@latest
```

`@main` installs the newest commit. `ccctl --version` then shows a Go
pseudo-version that names the commit.

### Direct download

Download `ccctl-<version>-darwin-<arch>.tar.gz` from the
[releases page](https://github.com/crux/ccctl/releases): `arm64` for
Apple silicon, `amd64` for Intel. The `.sha256` file next to it shows
that the download is complete. It does not prove where the file comes
from.

ccctl is not notarized. After a download with a browser, macOS blocks
the first start. Remove the quarantine flag once:

```sh
xattr -d com.apple.quarantine ./ccctl
```

A download with `curl` does not set the flag.

## Shell completion

Homebrew installs the completion for you. If zsh does not load it, see
[Homebrew shell completion](https://docs.brew.sh/Shell-Completion).

For other installs, add one line to your shell configuration. The line
runs at every shell start, so the completion always matches the
installed ccctl.

```sh
# zsh: ~/.zshrc
command -v ccctl >/dev/null && source <(ccctl completion zsh)

# bash: ~/.bashrc (needs the bash-completion package)
command -v ccctl >/dev/null && source <(ccctl completion bash)

# fish: ~/.config/fish/config.fish
command -q ccctl; and ccctl completion fish | source
```

## Platform support

ccctl runs on macOS.

**Linux: not released yet.** Most commands read files that Claude Code
writes in the same way on every platform, so they should work. Three
things block a release:

- `focus` can only switch tmux panes. iTerm2 and Terminal.app exist only
  on macOS, and ccctl does not raise windows under X11 or Wayland.
- `archive` uses `lsof` to check for open files, and so does the
  fallback for older Claude Code versions. Not every Linux system has
  `lsof`.
- Nobody has run ccctl on Linux yet. CI builds and tests it there.

**Windows: not supported.** ccctl finds Claude Code processes with `ps`
and `lsof`, and `focus` works with macOS terminals and tmux. Windows has
none of these. `move` also uses Unix file-system calls.

## Development

```sh
make help     # list the targets
make build    # bin/ccctl for this machine
make check    # go vet, tests, gofmt
make install  # the dev build into $(go env GOPATH)/bin
```

A dev build reports a Go pseudo-version: the last release, the commit
time and the commit, plus `+dirty` for uncommitted changes, for example
`ccctl version 0.1.1-0.20261102093000-abcdef123456+dirty`.

If Homebrew installed ccctl too, its copy usually comes first in PATH.
`brew unlink ccctl` lets the dev build win; `brew link ccctl` switches
back. `which -a ccctl` shows which copies exist.

## Releasing

For maintainers. Releases come only from CI.

1. Commit and push to `main`.
2. Run `make release VERSION=x.y.z`. The target checks the repo, creates
   the tag `vx.y.z` and pushes it.
3. The `Release` workflow runs the checks, builds the macOS archives,
   creates the GitHub release with notes from the commit log, and
   updates the Homebrew formula in `crux/homebrew-tap`.

Secret: `HOMEBREW_TAP_TOKEN`, a fine-grained token with Contents read and
write on `crux/homebrew-tap` only. It expires on 2027-01-30. Before that,
create a new token and set it on every repo that publishes to the tap
(`gh secret set HOMEBREW_TAP_TOKEN --repo crux/<repo>`).

The moving parts: `Makefile`, `release.env`, `scripts/`,
`.github/workflows/`.

## License

MIT. See [LICENSE](LICENSE).
