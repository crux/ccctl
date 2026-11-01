package main

import (
	"github.com/crux/ccctl/internal/tui"
	"github.com/spf13/cobra"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Open the ccctl TUI (Bubble Tea)",
	Long: `Open the ccctl TUI — a resource-oriented view of Claude Code
projects, sessions, and active runs with k9s-style modal navigation.

Views:
  :projects   list of ~/.claude/projects/ namespaces (default)
  :sessions   sessions in the focused project
  :active     currently-running claude processes (live refresh)

Keys:
  1 / 2 / 3, :<view>   switch view
  tab, h / l           switch pane
  j / k, g / G         move, jump to top / bottom
  enter                :projects → open its sessions
                       :sessions → focus the running session, or resume it
                       :active   → focus the session
  a / r                archive / restore the session under the cursor
  A                    show or hide archived sessions
  + / -, < / >         resize the panes
  q                    quit`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return tui.Run() },
}
