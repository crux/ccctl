package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crux/claudectl/internal/proc"
	"github.com/crux/claudectl/internal/project"
	"github.com/crux/claudectl/internal/search"
	"github.com/crux/claudectl/internal/session"
	"github.com/spf13/cobra"
)

var (
	findHere          bool
	findCaseSensitive bool
	findBrief         bool
	findLastN         int
)

var findCmd = &cobra.Command{
	Use:   "find [--here | <dir>] [flags] <pattern>",
	Short: "Content search across session conversations",
	Long: `Search user and assistant message text across Claude Code sessions.

Default scope is all projects. Use --here to restrict to the current
project's sessions, or pass a project directory as a positional arg to
restrict to that one.

Case-insensitive substring match by default (use -s for case-sensitive).

Output: one row per matching session with mtime, match count, UUID,
project and title. The 'M' column is the total number of pattern
occurrences in user+assistant text — a rough signal of how much the
topic was discussed. Below each row, a snippet from the last match in
the session; --last N shows the last N snippets in chronological order.
--brief drops the snippets but keeps the table.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 || len(args) == 2 {
			return nil
		}
		return fmt.Errorf("expected <pattern> or <dir> <pattern>; got %d args", len(args))
	},
	RunE: runFind,
}

func init() {
	findCmd.Flags().BoolVar(&findHere, "here", false, "restrict search to the current project")
	findCmd.Flags().BoolVarP(&findCaseSensitive, "case-sensitive", "s", false, "respect case (default is case-insensitive)")
	findCmd.Flags().BoolVar(&findBrief, "brief", false, "drop snippets; keep table and match count")
	findCmd.Flags().IntVar(&findLastN, "last", 1, "show last N snippets per session, chronological")
}

func runFind(cmd *cobra.Command, args []string) error {
	var pattern string
	var dir string
	switch len(args) {
	case 1:
		pattern = args[0]
	case 2:
		dir = args[0]
		pattern = args[1]
	}
	if findHere && dir != "" {
		return fmt.Errorf("--here and a positional <dir> are mutually exclusive")
	}

	opts := search.Opts{
		Pattern:       pattern,
		CaseSensitive: findCaseSensitive,
		LastN:         findLastN,
	}
	switch {
	case findHere:
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return err
		}
		opts.ProjectDir = project.Dir(real)
	case dir != "":
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return err
		}
		opts.ProjectDir = project.Dir(real)
	}

	hits, err := search.Search(opts)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Printf("No sessions match: %s\n", pattern)
		return nil
	}

	// Enrich each hit with authoritative title/cwd from PIDSession
	// (live sessions) or session.ReadMeta (closed). Scoped to the
	// subset of sessions that actually matched, so the extra I/O
	// stays proportional to results, not corpus size.
	for i := range hits {
		enrichHit(&hits[i])
	}

	printHits(os.Stdout, hits, findBrief)
	return nil
}

// enrichHit fills in Title and ProjectDir using the best available
// source. PIDSession (if the session is currently open by a running
// claude process) is authoritative for both. For closed sessions,
// session.ReadMeta provides the title via its reverse-chunk scan;
// ProjectDir may remain whatever the .jsonl itself revealed.
func enrichHit(h *search.Hit) {
	if ps, _ := proc.FindPIDBySessionID(h.UUID); ps != nil {
		if ps.Name != "" {
			h.Title = ps.Name
		}
		if ps.Cwd != "" {
			h.ProjectDir = ps.Cwd
		}
		return
	}
	if h.Title == "" && h.Path != "" {
		if meta, err := session.ReadMeta(h.Path); err == nil {
			if meta.CustomTitle != "" {
				h.Title = meta.CustomTitle
			} else if meta.Summary != "" {
				h.Title = meta.Summary
			}
		}
	}
}

// printHits emits the formatted table. Hand-rolled rather than
// tabwriter so snippets can be indented a fixed amount from the left
// margin without being pushed around by title width.
func printHits(w *os.File, hits []search.Hit, brief bool) {
	const (
		projMax  = 32
		titleMax = 0 // 0 = no truncation
	)

	// Header
	fmt.Fprintf(w, "%-16s  %6s  %-36s  %-*s  %s\n",
		"MTIME", "M", "UUID", projMax, "PROJECT", "TITLE")

	for _, h := range hits {
		title := h.Title
		if title == "" {
			title = "(no title)"
		}
		fmt.Fprintf(w, "%-16s  %6d  %-36s  %-*s  %s\n",
			h.MTime.Format("2006-01-02 15:04"),
			h.MatchCount,
			h.UUID,
			projMax, truncate(shortProject(h.ProjectDir), projMax),
			title)
		if !brief {
			for _, s := range h.Snippets {
				fmt.Fprintf(w, "    %s: %q\n", s.Role, s.Text)
			}
		}
	}
}

// shortProject condenses a cwd like /Users/x/work/src/shop into
// "work/src/shop" by taking the final two path components, mirroring
// the shortening used by `ps` and `sessions`.
func shortProject(p string) string {
	if p == "" {
		return "-"
	}
	parent := filepath.Base(filepath.Dir(p))
	base := filepath.Base(p)
	if parent == "." || parent == "/" {
		return base
	}
	return parent + "/" + base
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
