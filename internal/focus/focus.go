// Package focus brings the terminal running a given Claude Code session
// to the front — tmux pane first, then the detected native terminal
// (iTerm2 / Terminal.app). Factored out of the CLI so the TUI can call
// the same logic without shelling out to `ccctl focus`.
package focus

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/crux/ccctl/internal/proc"
)

// Location bundles everything a locator can determine about a running
// Claude Code session: its PIDSession, the process ancestor chain, the
// detected terminal emulator (if any), and the tmux pane coordinates
// (if the process runs under tmux).
type Location struct {
	Session    *proc.PIDSession
	Ancestors  []proc.Ancestor
	Terminal   string // display name (Terminal.app, iTerm2, …); "" if unknown
	TmuxCoords string // session:window.pane; "" if not under tmux
}

// Locate resolves a session UUID to a Location. Returns an error if the
// session is not currently open by any running claude process.
func Locate(uuid string) (*Location, error) {
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
	return &Location{
		Session:    ps,
		Ancestors:  ancestors,
		Terminal:   terminal,
		TmuxCoords: tmuxCoords,
	}, nil
}

// ByUUID is the single entry point callers want: locate the session,
// then bring its terminal (tmux pane, iTerm2 session, or Terminal.app
// tab) to the front.
func ByUUID(uuid string) error {
	loc, err := Locate(uuid)
	if err != nil {
		return err
	}
	// tmux takes priority: even inside iTerm2/Terminal, the authoritative
	// place to focus is the tmux pane holding the claude process.
	if loc.TmuxCoords != "" {
		return focusTmux(loc.TmuxCoords)
	}
	switch loc.Terminal {
	case "iTerm2":
		return focusITerm2(loc.Session.PID)
	case "Terminal.app":
		return focusTerminalApp(loc.Session.PID)
	case "":
		return fmt.Errorf("no supported terminal detected in ancestry for session %s", loc.Session.SessionID)
	default:
		return fmt.Errorf("focus not implemented for %s (stub)", loc.Terminal)
	}
}

// --- Terminal identification helpers ---

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

// --- Terminal focus actuators ---

func focusTmux(coords string) error {
	if err := exec.Command("tmux", "switch-client", "-t", coords).Run(); err != nil {
		return fmt.Errorf("tmux switch-client -t %s: %w", coords, err)
	}
	return nil
}

func focusITerm2(pid int) error {
	tty, err := getTTY(pid)
	if err != nil {
		return err
	}
	// Per-window / per-tab `try` blocks: iTerm2's `tabs of w` and
	// `sessions of t` are lazily enumerated and sometimes report counts
	// that don't match the addressable items (closed splits, zombie
	// panes). Without the try, a successful match + return still leaves
	// osascript surfacing the deferred index error from the next
	// iteration cycle.
	script := `tell application "iTerm2"
    activate
    repeat with w in windows
        try
            repeat with t in tabs of w
                try
                    repeat with s in sessions of t
                        if tty of s is "` + tty + `" then
                            select w
                            select t
                            select s
                            return
                        end if
                    end repeat
                end try
            end repeat
        end try
    end repeat
end tell`
	return runOsascript(script)
}

func focusTerminalApp(pid int) error {
	tty, err := getTTY(pid)
	if err != nil {
		return err
	}
	script := `tell application "Terminal"
    activate
    repeat with w in windows
        repeat with t in tabs of w
            if tty of t is "` + tty + `" then
                set index of w to 1
                set selected of t to true
                return
            end if
        end repeat
    end repeat
end tell`
	return runOsascript(script)
}

func runOsascript(script string) error {
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("osascript: %w", err)
		}
		return fmt.Errorf("osascript: %w (%s)", err, msg)
	}
	return nil
}

// getTTY returns the controlling-TTY device path for a PID (e.g.
// "/dev/ttys042") in the form iTerm2 and Terminal.app's AppleScript
// uses. macOS `ps -p N -o tty=` reports the tty either as "ttys042"
// (with "tty" prefix, observed on current macOS) or "s042" (without);
// both forms are normalised to "/dev/ttys042".
func getTTY(pid int) (string, error) {
	raw, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "tty=").Output()
	if err != nil {
		return "", fmt.Errorf("ps -o tty=: %w", err)
	}
	tty := strings.TrimSpace(string(raw))
	if tty == "" || tty == "?" || tty == "??" {
		return "", fmt.Errorf("pid %d has no controlling TTY", pid)
	}
	if strings.HasPrefix(tty, "/dev/") {
		return tty, nil
	}
	if !strings.HasPrefix(tty, "tty") {
		tty = "tty" + tty
	}
	return "/dev/" + tty, nil
}
