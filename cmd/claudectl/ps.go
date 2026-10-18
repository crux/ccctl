package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/crux/claudectl/internal/proc"
	"github.com/crux/claudectl/internal/session"
	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running Claude Code processes",
	Long: `List running claude processes with PID, working directory, session
UUID, and title.

Session UUID is the actual session the process has open, resolved in
this order:
  1. --resume <uuid> on the process command line (if present)
  2. .jsonl file the process has open (via lsof) — authoritative
  3. newest .jsonl in the project directory for the process's cwd
     (last-resort guess; may be wrong if multiple claude instances
     share one project).`,
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
	for _, p := range procs {
		title := "(no title)"
		if p.SessionFile != "" {
			if meta, err := session.ReadMeta(p.SessionFile); err == nil {
				title = meta.Display()
			}
		}
		var uuid string
		switch {
		case p.UUID == "":
			uuid = " -"
		case p.GuessedUUID:
			uuid = "~" + p.UUID
		default:
			uuid = " " + p.UUID
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", p.PID, p.Command, shortDir(p.Cwd), uuid, title)
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
