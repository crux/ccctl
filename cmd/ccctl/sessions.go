package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
	"github.com/spf13/cobra"
)

var sessionsArchived bool

var sessionsCmd = &cobra.Command{
	Use:   "sessions [dir]",
	Short: "List stored sessions for a project directory, newest first",
	Long: `List sessions stored for a project directory under
~/.claude/projects/<encoded-project>/. Default is the current working
directory. Live sessions only by default; -a / --archived also lists
sessions previously packed with 'ccctl archive' (marked
"[archived]").`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSessions,
}

func init() {
	sessionsCmd.Flags().BoolVarP(&sessionsArchived, "archived", "a", false, "include archived (.zip) sessions")
}

func runSessions(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	realDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return err
	}

	projectDir := project.Dir(realDir)
	if _, err := os.Stat(projectDir); err != nil {
		fmt.Printf("No Claude sessions found for: %s\n", realDir)
		return nil
	}

	// Fetch active-session UUIDs in parallel with ListInProject — the
	// ps/lsof calls underneath take roughly as long as all the file I/O,
	// so overlapping them keeps total latency close to max(both).
	activeChan := make(chan proc.ActiveSet, 1)
	go func() {
		set, _ := proc.ActiveUUIDs()
		activeChan <- set
	}()

	rows, err := session.ListInProject(projectDir, sessionsArchived)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	active := <-activeChan

	if len(rows) == 0 {
		fmt.Printf("No Claude sessions found for: %s\n", realDir)
		return nil
	}

	fmt.Printf("Claude sessions for: %s\n\n", realDir)
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	// Leading 1-char gutter: '*' confident-active, '~' guessed-active,
	// ' ' otherwise. Fixed left-margin position makes it scannable.
	// SIZE is the on-disk .jsonl (or .zip) size — a cheap, free-from-
	// directory-walk proxy for session weight. When the FTS index lands
	// it can be replaced or complemented with precomputed message counts.
	fmt.Fprintln(w, " \tMTIME\tSIZE\tUUID\tTITLE")
	for _, r := range rows {
		marker := " "
		switch {
		case active.Confident[r.UUID]:
			marker = "*"
		case active.Guessed[r.UUID]:
			marker = "~"
		}
		suffix := ""
		switch {
		case r.Archived:
			suffix = "  [archived]"
		case active.Confident[r.UUID]:
			// For live sessions, append (status) when non-idle. Idle
			// is the common state and left unmarked.
			if st := active.Status[r.UUID]; st != "" && st != "idle" {
				suffix = "  (" + st + ")"
			}
		}
		// humanBytesCell returns a fixed-width, decimal-aligned string
		// so digits, decimal points and unit letter all stack
		// vertically down the SIZE column.
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s%s\n",
			marker,
			r.MTime.Format("2006-01-02 15:04"),
			humanBytesCell(r.Size),
			r.UUID,
			r.Meta.Display(),
			suffix)
	}
	return w.Flush()
}
