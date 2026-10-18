package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/crux/claudectl/internal/proc"
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
  - the tmux pane coordinates, when the process runs under tmux`,
	Args: cobra.ExactArgs(1),
	RunE: runWhere,
}

func runWhere(cmd *cobra.Command, args []string) error {
	uuid := args[0]
	ps, err := proc.FindPIDBySessionID(uuid)
	if err != nil {
		return err
	}
	if ps == nil {
		return fmt.Errorf("no running claude process has session %s open", uuid)
	}

	ancestors, err := proc.PPIDAncestors(ps.PID)
	if err != nil {
		return err
	}
	terminal, tmuxCoords := identifyTerminal(ancestors, ps.PID)

	fmt.Printf("Session:     %s\n", uuid)
	fmt.Printf("  Title:     %s\n", ps.Name)
	fmt.Printf("  Status:    %s\n", ps.Status)
	fmt.Printf("  Version:   %s\n", ps.Version)
	fmt.Printf("  PID:       %d\n", ps.PID)
	fmt.Printf("  cwd:       %s\n", ps.Cwd)
	fmt.Println()
	fmt.Println("Process ancestry:")
	for _, a := range ancestors {
		fmt.Printf("  %6d  %s\n", a.PID, filepath.Base(a.Command))
	}
	if terminal != "" {
		fmt.Println()
		fmt.Printf("Terminal:    %s\n", terminal)
	}
	if tmuxCoords != "" {
		fmt.Printf("tmux pane:   %s\n", tmuxCoords)
		fmt.Printf("focus with:  tmux switch-client -t %s\n", tmuxCoords)
	}
	return nil
}

// knownTerminals maps the basename of a terminal-emulator process to the
// display name we show. Includes only the set we claim to recognise.
var knownTerminals = map[string]string{
	"Terminal":  "Terminal.app",
	"iTerm2":    "iTerm2",
	"iTerm":     "iTerm2",
	"WezTerm":   "WezTerm",
	"wezterm":   "WezTerm",
	"wezterm-gui": "WezTerm",
	"alacritty": "Alacritty",
	"kitty":     "kitty",
	"hyper":     "Hyper",
	"Hyper":     "Hyper",
}

// identifyTerminal walks the ancestor chain looking for a known terminal
// emulator and, if tmux appears in the chain, resolves the tmux pane
// coordinates holding the given PID.
func identifyTerminal(ancestors []proc.Ancestor, claudePID int) (terminal, tmuxCoords string) {
	var sawTmux bool
	for _, a := range ancestors {
		base := filepath.Base(a.Command)
		if base == "tmux" || strings.HasPrefix(a.Command, "tmux:") {
			sawTmux = true
			continue
		}
		if display := matchTerminal(base); display != "" && terminal == "" {
			terminal = display
		}
	}
	if sawTmux {
		tmuxCoords = findTmuxPaneCoords(claudePID, ancestors)
	}
	return terminal, tmuxCoords
}

// matchTerminal maps a process basename to a terminal display name,
// trying exact matches first and then a few prefix patterns for terminals
// that embed a version in the binary name (e.g. "iTermServer-3.7.1").
func matchTerminal(base string) string {
	if d, ok := knownTerminals[base]; ok {
		return d
	}
	switch {
	case strings.HasPrefix(base, "iTermServer"):
		return "iTerm2"
	case strings.HasPrefix(base, "wezterm-gui"):
		return "WezTerm"
	}
	return ""
}

// findTmuxPaneCoords matches any ancestor PID (plus the claude PID
// itself) against tmux's pane_pid list and returns the first match's
// session:window.pane coordinates. Returns "" if no match.
func findTmuxPaneCoords(claudePID int, ancestors []proc.Ancestor) string {
	raw, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{pane_pid} #{session_name}:#{window_index}.#{pane_index}").Output()
	if err != nil {
		return ""
	}
	candidates := map[int]bool{claudePID: true}
	for _, a := range ancestors {
		candidates[a.PID] = true
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		panePID, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if candidates[panePID] {
			return fields[1]
		}
	}
	return ""
}
