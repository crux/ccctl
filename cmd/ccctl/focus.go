package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

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

For other terminals, falls through with a 'not implemented' error and
points at 'ccctl where' for a manual lookup.

On first use, macOS will prompt to grant ccctl permission to
control Terminal.app / iTerm2 via osascript — one-time dialog.`,
	Args: cobra.ExactArgs(1),
	RunE: runFocus,
}

func runFocus(cmd *cobra.Command, args []string) error {
	loc, err := LocateSession(args[0])
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
		return fmt.Errorf("no supported terminal detected in ancestry; see `ccctl where %s`", loc.Session.SessionID)
	default:
		return fmt.Errorf("focus not implemented for %s (stub); see `ccctl where %s`",
			loc.Terminal, loc.Session.SessionID)
	}
}

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
	script := `tell application "iTerm2"
    activate
    repeat with w in windows
        repeat with t in tabs of w
            repeat with s in sessions of t
                if tty of s is "` + tty + `" then
                    select w
                    select t
                    select s
                    return
                end if
            end repeat
        end repeat
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
