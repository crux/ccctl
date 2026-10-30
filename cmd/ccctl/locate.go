package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/crux/ccctl/internal/proc"
)

// SessionLocation bundles everything a locator can determine about a
// running Claude Code session: its PIDSession, the process ancestor
// chain, the detected terminal emulator (if any), and the tmux pane
// coordinates (if the process runs under tmux). Used by `focus`.
type SessionLocation struct {
	Session    *proc.PIDSession
	Ancestors  []proc.Ancestor
	Terminal   string // display name (Terminal.app, iTerm2, …); "" if unknown
	TmuxCoords string // session:window.pane; "" if not under tmux
}

// LocateSession resolves a session UUID to a SessionLocation. Returns
// an error if the session is not currently open by any running claude
// process.
func LocateSession(uuid string) (*SessionLocation, error) {
	ps, err := proc.FindPIDBySessionID(uuid)
	if err != nil {
		return nil, err
	}
	if ps == nil {
		return nil, fmt.Errorf("no running claude process has session %s open", uuid)
	}
	ancestors, err := proc.PPIDAncestors(ps.PID)
	if err != nil {
		return nil, err
	}
	terminal, tmuxCoords := identifyTerminal(ancestors, ps.PID)
	return &SessionLocation{
		Session:    ps,
		Ancestors:  ancestors,
		Terminal:   terminal,
		TmuxCoords: tmuxCoords,
	}, nil
}

// --- Terminal identification helpers (used by LocateSession) ---

// knownTerminals maps the basename of a terminal-emulator process to the
// display name we show. Includes only the set we claim to recognise.
var knownTerminals = map[string]string{
	"Terminal":    "Terminal.app",
	"iTerm2":      "iTerm2",
	"iTerm":       "iTerm2",
	"WezTerm":     "WezTerm",
	"wezterm":     "WezTerm",
	"wezterm-gui": "WezTerm",
	"alacritty":   "Alacritty",
	"kitty":       "kitty",
	"hyper":       "Hyper",
	"Hyper":       "Hyper",
}

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
// trying exact matches first and then prefix patterns for terminals
// that embed a version in the binary name (iTermServer-3.7.1, etc.).
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
