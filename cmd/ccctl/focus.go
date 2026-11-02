package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/crux/ccctl/internal/focus"
	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/session"
	"github.com/spf13/cobra"
)

var focusCmd = &cobra.Command{
	Use:   "focus <session-uuid>",
	Short: "Bring the terminal running a given session to the front",
	Long: `Focus the terminal (and, when under tmux, the specific pane)
running a given Claude Code session. Supported terminals:

  - tmux          (any outer terminal)
  - Terminal.app  (macOS native)
  - iTerm2        (macOS)

For other terminals, falls through with a 'not implemented' error.

A background session has no terminal of its own. When a 'claude
attach' shows it, focus brings that terminal to the front. Otherwise
focus prints the 'claude attach <id>' line that opens it.

On first use, macOS will prompt to grant ccctl permission to
control Terminal.app / iTerm2 via osascript — one-time dialog.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: activeSessionCompleter,
	RunE:              runFocus,
}

// activeSessionCompleter returns UUIDs of currently-running claude
// sessions, each with a title + cwd-basename + status description.
// Bounded by number of live claude processes, so ReadMeta fallback for
// an empty PIDSession.Name is cheap in practice.
func activeSessionCompleter(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	infos, err := proc.ListSessions()
	if err != nil || len(infos) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		if info.UUID == "" || !strings.HasPrefix(info.UUID, toComplete) {
			continue
		}
		title := info.Title
		if title == "" && info.SessionFile != "" {
			if m, err := session.ReadMeta(info.SessionFile); err == nil {
				title = m.Display()
			}
		}
		if title == "" {
			title = "(no title)"
		}
		desc := title
		if info.Cwd != "" {
			desc += " · " + filepath.Base(info.Cwd)
		}
		switch {
		case info.Background && info.Status != "":
			desc += " [" + info.Status + ", background]"
		case info.Background:
			desc += " [background]"
		case info.Status != "":
			desc += " [" + info.Status + "]"
		}
		out = append(out, info.UUID+"\t"+desc)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func runFocus(cmd *cobra.Command, args []string) error {
	err := focus.ByUUID(args[0])
	var bg *focus.BackgroundError
	if errors.As(err, &bg) {
		fmt.Printf("%s runs in the background, no terminal shows it.\nOpen it in this terminal: claude attach %s\n", shortID(args[0]), bg.ID)
		return nil
	}
	return err
}

// shortID returns the first 8 characters of a session id.
func shortID(uuid string) string {
	if len(uuid) > 8 {
		return uuid[:8]
	}
	return uuid
}
