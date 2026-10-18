package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

var whereCmd = &cobra.Command{
	Use:   "where <session-uuid>",
	Short: "Locate the terminal running a given session",
	Long: `Locate the terminal (and tmux pane, when applicable) running a
given Claude Code session. The session must be currently open by a
running claude process.

Prints:
  - the session's PID, cwd, version, status
  - the ancestor process chain
  - the detected terminal app (Terminal, iTerm2, WezTerm, etc.)
  - the tmux pane coordinates, when the process runs under tmux

Prose output, read-only. For action (jumping to the terminal), see
'claudectl focus'.`,
	Args: cobra.ExactArgs(1),
	RunE: runWhere,
}

func runWhere(cmd *cobra.Command, args []string) error {
	loc, err := LocateSession(args[0])
	if err != nil {
		return err
	}
	ps := loc.Session

	fmt.Printf("Session:     %s\n", ps.SessionID)
	fmt.Printf("  Title:     %s\n", ps.Name)
	fmt.Printf("  Status:    %s\n", ps.Status)
	fmt.Printf("  Version:   %s\n", ps.Version)
	fmt.Printf("  PID:       %d\n", ps.PID)
	fmt.Printf("  cwd:       %s\n", ps.Cwd)
	fmt.Println()
	fmt.Println("Process ancestry:")
	for _, a := range loc.Ancestors {
		fmt.Printf("  %6d  %s\n", a.PID, filepath.Base(a.Command))
	}
	if loc.Terminal != "" {
		fmt.Println()
		fmt.Printf("Terminal:    %s\n", loc.Terminal)
	}
	if loc.TmuxCoords != "" {
		fmt.Printf("tmux pane:   %s\n", loc.TmuxCoords)
		fmt.Printf("focus with:  tmux switch-client -t %s\n", loc.TmuxCoords)
	}
	return nil
}
