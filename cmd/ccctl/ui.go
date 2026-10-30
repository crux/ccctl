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

Switch views with ':<name>' or the 1 / 2 / 3 shortcuts. 'q' quits.

Step 1 scaffold: layout + navigation only. Data wiring and resume
actions arrive in later steps.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return tui.Run() },
}
