package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/crux/claudectl/internal/proc"
	"github.com/crux/claudectl/internal/project"
	"github.com/crux/claudectl/internal/search"
	"github.com/crux/claudectl/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	findHere          bool
	findCaseSensitive bool
	findBrief         bool
	findLastN         int
	findUUID          string
	findColor         string
)

var findCmd = &cobra.Command{
	Use:   "find [--here | <dir>] [flags] <pattern>",
	Short: "Content search across session conversations",
	Long: `Search user and assistant message text across Claude Code sessions.

Default scope is all projects. Use --here to restrict to the current
project's sessions, or pass a project directory as a positional arg to
restrict to that one. Use --uuid to scope the search to a single
session (prefix match on the UUID is enough).

Case-insensitive substring match by default (use -s for case-sensitive).

Output: one row per matching session with a leading active marker
("*" for a running claude process on that session, "~" for the
newest-in-dir guess fallback), mtime, match count, UUID, project and
title. The 'M' column is the total number of pattern occurrences in
user+assistant text — a rough signal of how much the topic was
discussed. Below each row, a snippet from the last match in the
session; --last N shows the last N snippets in chronological order.
--brief drops the snippets but keeps the table.

Matches inside snippets are highlighted when stdout is a terminal; use
--color always|never|auto (default auto) to override.`,
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
	findCmd.Flags().StringVar(&findUUID, "uuid", "", "scope to one session (UUID prefix is enough)")
	findCmd.Flags().StringVar(&findColor, "color", "auto", "highlight matches in snippets: always|never|auto")
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
		UUIDPrefix:    findUUID,
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

	for i := range hits {
		enrichHit(&hits[i])
	}

	confident, guessed, _ := proc.ActiveUUIDs()
	useColor := resolveColor(findColor)
	printHits(os.Stdout, hits, pattern, findCaseSensitive, findBrief, useColor, confident, guessed)
	return nil
}

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

// Column layout constants, used for both the data row and the snippet
// hanging-indent. UUID starts at column snippetStartCol in the data row;
// snippets wrap in the "cell" to the right of that column.
const (
	findProjMax      = 32
	findSnippetStart = 29 // column where UUID starts; snippet text aligns here
	findMinWrapWidth = 20 // if terminal narrower than this past snippetStart, don't wrap
)

func printHits(w *os.File, hits []search.Hit, pattern string, caseSensitive, brief, color bool, confident, guessed map[string]bool) {
	// Terminal width: detect if stdout is a TTY. For non-TTYs (pipe,
	// redirect), skip wrapping so downstream tools get stable lines.
	termWidth := 0
	if fi, err := os.Stdout.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		if cols, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			termWidth = cols
		}
	}

	// Column widths picked so headers align with data rows. Leading
	// gutter is one char for the active marker.
	fmt.Fprintf(w, "   %-16s  %6s  %-36s  %-*s  %s\n",
		"MTIME", "M", "UUID", findProjMax, "PROJECT", "TITLE")

	var hl *regexp.Regexp
	if color {
		flags := "(?i)"
		if caseSensitive {
			flags = ""
		}
		hl = regexp.MustCompile(flags + regexp.QuoteMeta(pattern))
	}

	for _, h := range hits {
		marker := " "
		switch {
		case confident[h.UUID]:
			marker = "*"
		case guessed[h.UUID]:
			marker = "~"
		}
		title := h.Title
		if title == "" {
			title = "(no title)"
		}
		fmt.Fprintf(w, "%s  %-16s  %6d  %-36s  %-*s  %s\n",
			marker,
			h.MTime.Format("2006-01-02 15:04"),
			h.MatchCount,
			h.UUID,
			findProjMax, truncate(shortProject(h.ProjectDir), findProjMax),
			title)
		if brief {
			continue
		}
		for _, s := range h.Snippets {
			text := s.Text
			if hl != nil {
				text = hl.ReplaceAllStringFunc(text, highlightMatch)
			}
			// Snippet text goes into the UUID-column cell. Role is
			// deliberately omitted for now (placement TBD) — the role
			// field on each Snippet remains available on Hit.Snippets
			// for a later layout change.
			lines := wrapSnippet(text, findSnippetStart, termWidth)
			for _, line := range lines {
				fmt.Fprintf(w, "%*s%s\n", findSnippetStart, "", line)
			}
		}
		// Blank line separates sessions when snippets are shown.
		fmt.Fprintln(w)
	}
}

// wrapSnippet breaks text into lines that each fit in the terminal
// starting at column startCol, so continuation lines align with the
// cell. Returns a single-element slice when wrapping isn't warranted
// (termWidth == 0 for non-TTY, or available width is pathologically
// narrow). Word-boundary wrap; a word longer than the available width
// is emitted on its own line and allowed to overflow / wrap naturally.
func wrapSnippet(text string, startCol, termWidth int) []string {
	if termWidth == 0 {
		return []string{text}
	}
	avail := termWidth - startCol
	if avail < findMinWrapWidth {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	var lines []string
	var curLine string
	var curWidth int
	for _, word := range words {
		ww := visualWidth(word)
		if curLine == "" {
			curLine = word
			curWidth = ww
			continue
		}
		// +1 for the space separator
		if curWidth+1+ww > avail {
			lines = append(lines, curLine)
			curLine = word
			curWidth = ww
		} else {
			curLine += " " + word
			curWidth += 1 + ww
		}
	}
	if curLine != "" {
		lines = append(lines, curLine)
	}
	return lines
}

// visualWidth returns the display width of s in cells, ignoring ANSI
// SGR escape sequences (which contribute 0 to display width).
var ansiStripRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visualWidth(s string) int {
	return utf8.RuneCountInString(ansiStripRE.ReplaceAllString(s, ""))
}

func highlightMatch(m string) string {
	return "\033[1;33m" + m + "\033[0m" // bold yellow
}

// resolveColor decides whether ANSI colors are safe to emit.
// Honours NO_COLOR for auto mode (standard convention).
func resolveColor(mode string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, _ := os.Stdout.Stat()
	return fi != nil && (fi.Mode()&os.ModeCharDevice) != 0
}

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

