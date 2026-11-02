package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/session"
	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running Claude Code processes",
	Long: `List running claude processes with PID, working directory, session
UUID, and title.

Session UUID is the actual session the process has open, resolved in
this order:
  1. the session record ~/.claude/sessions/<pid>.json — authoritative
  2. --resume <uuid> on the process command line (older Claude Code)
  3. newest .jsonl in the project directory for the process's cwd
     (last-resort guess, marked with ~; may be wrong if multiple
     claude instances share one project).

Background sessions (claude --bg, or a session sent to the background)
run under a daemon with helper processes. Each helper is listed under
the session or daemon it belongs to, marked with └ and a short note.
'claude attach <id>' opens a background session in a terminal.`,
	RunE: runPs,
}

func runPs(cmd *cobra.Command, args []string) error {
	procs, err := proc.ListClaudeProcesses()
	if err != nil {
		return fmt.Errorf("list processes: %w", err)
	}
	if len(procs) == 0 {
		fmt.Println("No running Claude instances found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	// The leading space on " UUID" keeps column alignment: every row's
	// UUID cell has a 1-char prefix (either '~' for a newest-in-project-dir
	// guess or ' ' when the UUID was resolved authoritatively), so the UUID
	// bodies start at the same column either way.
	fmt.Fprintln(w, "PID\tVERSION\tDIR\t UUID\tTITLE")
	for _, r := range proc.Arrange(procs) {
		pid := strconv.Itoa(r.PID)
		if r.Sub {
			pid = "└ " + pid
		}
		version := r.Command
		if version == "" {
			version = "-"
		}
		if r.Role != proc.RoleSession {
			role := strings.TrimPrefix(r.Label(), "└ ")
			fmt.Fprintf(w, "%s\t%s\t-\t -\t%s: %s\n", pid, version, role, r.Note)
			continue
		}
		title := r.Title
		if title == "" && r.SessionFile != "" {
			// Fallback for legacy-path PIDs where the title didn't come
			// from PIDSession.Name — read the .jsonl ourselves.
			if meta, err := session.ReadMeta(r.SessionFile); err == nil {
				title = meta.Display()
			}
		}
		if title == "" {
			title = "(no title)"
		}
		// Mark a session that is actively processing, and one that
		// runs in the background. "idle" is the common state and left
		// unmarked to keep the output quiet; any other status is shown
		// verbatim so future statuses surface automatically.
		var marks []string
		if r.Status != "" && r.Status != "idle" {
			marks = append(marks, r.Status)
		}
		if r.Background {
			marks = append(marks, "background: claude attach "+r.AttachID())
		}
		if len(marks) > 0 {
			title += "  (" + strings.Join(marks, ", ") + ")"
		}
		var uuid string
		switch {
		case r.UUID == "":
			uuid = " -"
		case r.GuessedUUID:
			uuid = "~" + r.UUID
		default:
			uuid = " " + r.UUID
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", pid, version, shortDir(r.Cwd), uuid, title)
	}
	return w.Flush()
}

// shortDir returns "parent/base" for a long path, or "-" for empty.
func shortDir(p string) string {
	if p == "" {
		return "-"
	}
	parent := filepath.Base(filepath.Dir(p))
	base := filepath.Base(p)
	return parent + "/" + base
}
