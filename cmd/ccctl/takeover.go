package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var takeoverCmd = &cobra.Command{
	Use:   "takeover <session-uuid>",
	Short: "Take a running session over into the current terminal (not yet implemented)",
	// Hidden until it is implemented: not in help, not in completion.
	Hidden: true,
	Long: `NOT YET IMPLEMENTED — stub for a planned future command.

The intent:

Resolve the given session UUID to its PID + cwd via
~/.claude/sessions/<pid>.json. Send SIGTERM to the old claude process
(bounded wait, SIGKILL on timeout). Then exec 'claude --resume <uuid>'
in the current shell, replacing it. The session continues in the
current terminal.

Use cases this would address:
  - The terminal running the session is gone (window closed, remote
    tmux dropped, VPN went down) and 'ccctl focus' cannot reach it.
  - The terminal is reachable but you'd rather continue in the current
    one — switching hosts, consolidating windows, etc.

Why deferred:
  'ccctl focus' handles the common case for the terminals currently
  supported (tmux, Terminal.app, iTerm2). Takeover is a destructive
  kill-and-respawn operation, needs careful confirmation UX, and would
  only matter when focus is not possible. Implement when a real need
  materialises.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("takeover is not yet implemented; run `ccctl takeover --help` for the planned behaviour")
	},
}
