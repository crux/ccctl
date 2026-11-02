// Package focus brings the terminal running a given Claude Code session
// to the front — tmux pane first, then the detected native terminal
// (iTerm2 / Terminal.app). Factored out of the CLI so the TUI can call
// the same logic without shelling out to `ccctl focus`.
package focus

import (
	"errors"
	"fmt"
	"os"
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

// BackgroundError says that a session runs in the background: no
// terminal shows it, so there is nothing to bring to the front.
// `claude attach <ID>` opens it in a terminal.
type BackgroundError struct {
	ID string // the id `claude attach` takes
}

func (e *BackgroundError) Error() string {
	return fmt.Sprintf("session runs in the background; open it with: claude attach %s", e.ID)
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
	loc, err := locatePID(ps.PID)
	if err != nil {
		return nil, err
	}
	loc.Session = ps
	return loc, nil
}

// locatePID finds the terminal that a process runs in.
func locatePID(pid int) (*Location, error) {
	ancestors, err := proc.PPIDAncestors(pid)
	if err != nil {
		return nil, err
	}
	terminal, tmuxCoords := identifyTerminal(ancestors, pid)
	return &Location{
		Ancestors:  ancestors,
		Terminal:   terminal,
		TmuxCoords: tmuxCoords,
	}, nil
}

// ByUUID is the single entry point callers want: locate the session,
// then bring its terminal (tmux pane, iTerm2 session, or Terminal.app
// tab) to the front.
//
// A background session has no terminal of its own. When a `claude
// attach` shows it in a terminal, ByUUID brings that terminal to the
// front. Otherwise it returns a *BackgroundError.
func ByUUID(uuid string) error {
	loc, err := Locate(uuid)
	if err != nil {
		return err
	}
	// A session without a terminal in its ancestry can be a background
	// session whose record does not say so; the process list knows.
	if loc.Session.Kind == "bg" || (loc.Terminal == "" && loc.TmuxCoords == "") {
		if err := background(uuid); err != errNotBackground {
			return err
		}
	}
	return focusAt(loc, loc.Session.PID, "session "+loc.Session.SessionID)
}

var errNotBackground = errors.New("not a background session")

// background focuses the terminal of a `claude attach` that shows the
// background session uuid, or returns a *BackgroundError. It returns
// errNotBackground for a session that does not run in the background.
func background(uuid string) error {
	infos, err := proc.ListClaudeProcesses()
	if err != nil {
		return err
	}
	var s *proc.Info
	for i := range infos {
		if infos[i].Role == proc.RoleSession && infos[i].UUID == uuid && infos[i].Background {
			s = &infos[i]
		}
	}
	if s == nil {
		return errNotBackground
	}
	for _, c := range infos {
		if s.Attaches(c.AttachTarget()) {
			return ByPID(c.PID)
		}
	}
	return &BackgroundError{ID: s.AttachID()}
}

// ByPID brings the terminal that runs process pid to the front.
func ByPID(pid int) error {
	loc, err := locatePID(pid)
	if err != nil {
		return err
	}
	return focusAt(loc, pid, fmt.Sprintf("pid %d", pid))
}

func focusAt(loc *Location, pid int, what string) error {
	// tmux takes priority: even inside iTerm2/Terminal, the authoritative
	// place to focus is the tmux pane holding the claude process.
	if loc.TmuxCoords != "" {
		return focusTmux(loc.TmuxCoords)
	}
	switch loc.Terminal {
	case "iTerm2":
		return focusITerm2(pid)
	case "Terminal.app":
		return focusTerminalApp(pid)
	case "":
		return fmt.Errorf("no supported terminal detected in ancestry for %s", what)
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
	if os.Getenv("CCCTL_FOCUS_DEBUG") != "" {
		return focusITerm2Debug(tty)
	}
	// Enumerate everything by numeric index, with a try around each
	// per-session tty read. `repeat with s in sessions of t` throws
	// "Can't get tty" the moment it hits a session in a weird transient
	// state and that error aborts the whole tab's enumeration — so a
	// match sitting AFTER the broken session never gets seen. Per-index
	// access with a per-session try keeps every session addressable.
	//
	// Capture the matched window as `id of w` (not the loop variable w,
	// which is a positional "item N of every window" reference that
	// iTerm2 refuses to resolve outside the enumeration — "Invalid
	// index" on tab lookups). Re-fetch via `window id tgtWinID` for the
	// three selects.
	script := `tell application "iTerm2"
    activate
    set tgtWinID to missing value
    set tgtTabIdx to 0
    set tgtSesIdx to 0
    repeat with w in windows
        set tabCount to 0
        try
            set tabCount to count of tabs of w
        end try
        repeat with tabIdx from 1 to tabCount
            set sesCount to 0
            try
                set sesCount to count of sessions of tab tabIdx of w
            end try
            repeat with sesIdx from 1 to sesCount
                set sTTY to ""
                try
                    set sTTY to tty of session sesIdx of tab tabIdx of w
                end try
                if sTTY is "` + tty + `" then
                    set tgtWinID to id of w
                    set tgtTabIdx to tabIdx
                    set tgtSesIdx to sesIdx
                    exit repeat
                end if
            end repeat
            if tgtWinID is not missing value then exit repeat
        end repeat
        if tgtWinID is not missing value then exit repeat
    end repeat
    if tgtWinID is not missing value then
        try
            tell window id tgtWinID
                select
                tell tab tgtTabIdx
                    select
                    tell session tgtSesIdx
                        select
                    end tell
                end tell
            end tell
        end try
    end if
end tell`
	return runOsascript(script)
}

// focusITerm2Debug runs the same enumeration but returns a verbose
// report instead of silently failing. Enabled via CCCTL_FOCUS_DEBUG=1
// so an operator can see exactly which window / tab / session matched
// and whether each select succeeded or threw. The 'error' return
// carries the report as a string so the CLI prints it.
func focusITerm2Debug(tty string) error {
	script := `tell application "iTerm2"
    activate
    set rep to "START tty=` + tty + `"
    set winIdx to 0
    repeat with w in windows
        set winIdx to winIdx + 1
        set tabCount to 0
        try
            set tabCount to count of tabs of w
        end try
        set rep to rep & " | win=" & winIdx & " tabs=" & tabCount
        repeat with tabIdx from 1 to tabCount
            set sesCount to 0
            try
                set sesCount to count of sessions of tab tabIdx of w
            end try
            set triedTTYs to ""
            repeat with sesIdx from 1 to sesCount
                set sTTY to "?"
                try
                    set sTTY to tty of session sesIdx of tab tabIdx of w
                end try
                set triedTTYs to triedTTYs & sTTY & ","
                if sTTY is "` + tty + `" then
                    set winID to id of w
                    set rep to rep & " | MATCH win=" & winIdx & " winID=" & winID & " tab=" & tabIdx & " ses=" & sesIdx
                    try
                        tell window id winID to select
                        set rep to rep & " / selW ok"
                    on error e1
                        set rep to rep & " / selW err: " & e1
                    end try
                    try
                        tell window id winID to tell tab tabIdx to select
                        set rep to rep & " / selT ok"
                    on error e2
                        set rep to rep & " / selT err: " & e2
                    end try
                    try
                        tell window id winID to tell tab tabIdx to tell session sesIdx to select
                        set rep to rep & " / selS ok"
                    on error e3
                        set rep to rep & " / selS err: " & e3
                    end try
                    return rep
                end if
            end repeat
            set rep to rep & " | tab=" & tabIdx & " tried=" & triedTTYs
        end repeat
    end repeat
    return rep & " | NO MATCH"
end tell`
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	report := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("osascript (debug): %w — %s", err, report)
	}
	return fmt.Errorf("DEBUG: %s", report)
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
