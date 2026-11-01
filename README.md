# ccctl

**Find, jump into and search your Claude Code sessions.**
Unofficial, not affiliated with Anthropic.

![ccctl ps lists the running sessions; ccctl focus with tab completion jumps into one](docs/demo/hero.gif)

```sh
brew install crux/tap/ccctl
```

## Where was that session?

```sh
ccctl ps            # every running Claude Code session
ccctl focus <Tab>   # pick one by its title, land in its terminal
```

`focus` brings the session to the front: the iTerm2 tab, the
Terminal.app window or the tmux pane.

## Search every conversation

![ccctl status shows 448 sessions and 925 MB; ccctl find searches all of them in a third of a second](docs/demo/find.gif)

```sh
ccctl find "rate limit"
```

## All of it in one place

![ccctl ui: browse projects and sessions, resume one, jump to a running one](docs/demo/ui.gif)

```sh
ccctl ui
```

Enter resumes a session, or jumps to it while it runs.
Next: search as you type, right in `ccctl ui`.

## Move and rename

```sh
ccctl move <session> <dir>    # a session into another project
ccctl move <dir> <new-path>   # a directory, with its sessions, memory, settings and prompt history
```

`--dry-run` shows the plan first.

## And more

| | |
|---|---|
| `ccctl status` | projects, sessions, size at a glance |
| `ccctl sessions [dir]` | the sessions of a directory |
| `ccctl projects` | every project folder |
| `ccctl archive`, `ccctl restore` | pack a session into a `.zip`, and back |

`ccctl <command> --help` shows the details.

## What ccctl changes

Most commands only read. These commands change files:

- `move <session> <dir>` moves the files of one session from one project
  folder to another.
- `move <dir> <new-path>` renames the directory and its project folders.
  It copies the directory's settings in `~/.claude.json` and changes the
  project path in `~/.claude/history.jsonl`. It edits both files under
  Claude Code's own file lock. It refuses while Claude Code runs inside
  the directory.
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

## Claude Code integration

`claude/commands/post-session-move-memory.md` is a Claude Code command.
After `ccctl move <session> <dir>`, run `/post-session-move-memory` in
the resumed session. It finds the memory files that the session wrote in
the old project folder, and helps you move, keep or remove each one.

```sh
make claude-install   # copy the command into ~/.claude/commands
make claude-check     # report a missing or changed copy
```

Without a clone of this repository (for example after a Homebrew
install), download the command directly:

```sh
mkdir -p ~/.claude/commands
curl -fsSL https://raw.githubusercontent.com/crux/ccctl/main/claude/commands/post-session-move-memory.md \
  -o ~/.claude/commands/post-session-move-memory.md
```

The repository holds the source. Change the command here, then run
`make claude-install`.

## Platform support

ccctl runs on macOS. It reads the files that Claude Code writes under
`~/.claude/`. Anthropic does not document them, so a Claude Code update
can change them. Tested with Claude Code 2.1.316 on macOS 27.

**Linux: not released yet.** Most commands read files that Claude Code
writes in the same way on every platform, so they should work. Three
things block a release:

- `focus` can only switch tmux panes. iTerm2 and Terminal.app exist only
  on macOS, and ccctl does not raise windows under X11 or Wayland.
- Older Claude Code versions write no session records. For them, ccctl
  falls back to `lsof`, which not every Linux system has. Current
  versions do not need it.
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
make demo     # render the GIFs in docs/demo/ (Docker; made-up sessions)
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
