package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/spf13/cobra"
)

var projectsAll bool

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List known Claude Code project namespaces, newest first",
	Long: `List project directories under ~/.claude/projects/ together
with a session + archive summary.

Columns:
  gutter      '*' when at least one session is currently open by a
              running claude process, ' ' otherwise
  LATEST      newest .jsonl or .zip mtime inside the project dir
  SIZE        sum of .jsonl + .zip file sizes
  SESSIONS    live .jsonl count
  ARCHIVED    .zip count
  PROJECT     real cwd read from any .jsonl's cwd field; falls back
              to the encoded project-dir name when no .jsonl can be
              read (archive-only projects, or empty)

Projects whose cwd lives under a system tmp directory
(/private/var/folders/*, /tmp/*, /private/tmp/*) — typically one-off
runs from a shell like 'tmp-claude' — are hidden by default. Pass
-a / --all to include them.`,
	Args: cobra.NoArgs,
	RunE: runProjects,
}

func init() {
	projectsCmd.Flags().BoolVarP(&projectsAll, "all", "a", false, "include system-tmp projects (/tmp, /private/tmp, /private/var/folders)")
}

func runProjects(cmd *cobra.Command, args []string) error {
	infos, err := project.ListProjects()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Println("No Claude projects found under ~/.claude/projects/.")
		return nil
	}

	// Resolve which project dirs host currently-active sessions.
	activeByDir := map[string]bool{}
	if active, err := proc.ActiveUUIDs(); err == nil {
		for uuid := range active.Confident {
			if path, _ := project.FindSessionFile(uuid); path != "" {
				activeByDir[filepath.Dir(path)] = true
			}
		}
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].Latest.After(infos[j].Latest) })

	home, _ := os.UserHomeDir()
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, " \tLATEST\tSIZE\tSESSIONS\tARCHIVED\tPROJECT")
	hidden := 0
	for _, p := range infos {
		if !projectsAll && project.IsTmp(p) {
			hidden++
			continue
		}
		marker := " "
		if activeByDir[p.Dir] {
			marker = "*"
		}
		displayPath := displayProject(p, home)
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\n",
			marker,
			p.Latest.Format("2006-01-02 15:04"),
			humanBytesCell(p.TotalSize),
			p.Sessions,
			p.Archived,
			displayPath)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if hidden > 0 {
		fmt.Printf("\n%d system-tmp projects hidden (-a to include).\n", hidden)
	}
	return nil
}

// displayProject picks a user-friendly string for a project row.
// Prefers the decoded cwd, abbreviating $HOME to '~' (per Dirk's
// presentation rule); falls back to the raw encoded dir name when the
// project holds no readable .jsonl.
func displayProject(p project.Info, home string) string {
	if p.Cwd == "" {
		return p.EncodedName
	}
	if home != "" && strings.HasPrefix(p.Cwd, home) {
		return "~" + strings.TrimPrefix(p.Cwd, home)
	}
	return p.Cwd
}
