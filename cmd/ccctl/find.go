package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/search"
	"github.com/crux/ccctl/internal/session"
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
	findTools         bool
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
	findCmd.Flags().BoolVar(&findTools, "tools", false, "also search tool_use payloads (not yet implemented)")
	// Hidden until it is implemented.
	_ = findCmd.Flags().MarkHidden("tools")
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
	if findTools {
		return fmt.Errorf("--tools is not yet implemented; find currently matches user/assistant text only")
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

	active, _ := proc.ActiveUUIDs()
	useColor := resolveColor(findColor)
	printHits(os.Stdout, hits, pattern, findCaseSensitive, findBrief, useColor, active)
	return nil
}

func enrichHit(h *search.Hit) {
	if ps, _ := proc.FindPIDBySessionID(h.UUID); ps != nil {
		if t := ps.Title(); t != "" {
			h.Title = t
		}
		if ps.Cwd != "" {
			h.ProjectDir = ps.Cwd
		}
	}
	if h.Title == "" && h.Path != "" {
		if meta, err := session.ReadMeta(h.Path); err == nil {
			h.Title = meta.Title()
		}
	}
}

// Column layout constants. Data row:
//
//	MTIME        col 3-18    (16 chars)
//	M            col 21-26   (6 chars, right-aligned)
//	PROJECT      col 29-60   (32 chars)
//	TITLE+UUID   col 63-...  (fused: "title (uuid)", wraps freely)
//
// Snippet cell spans PROJECT+TITLE+UUID (col 29 to terminal edge).
// Role char at col 27 (in the pad between M and PROJECT), snippet
// text starts at col 29 and wraps back to that column.
const (
	findProjMax       = 32
	findTitleStart    = 63
	findSnippetStart  = 29 // PROJECT start — snippet text column
	findRolePrefixCol = 27 // role char lives at snippet start - 2
	findMinWrapWidth  = 20
)

func printHits(w *os.File, hits []search.Hit, pattern string, caseSensitive, brief, color bool, active proc.ActiveSet) {
	// Terminal width: detect if stdout is a TTY. For non-TTYs (pipe,
	// redirect), skip wrapping so downstream tools get stable lines.
	termWidth := 0
	if fi, err := os.Stdout.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		if cols, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			termWidth = cols
		}
	}

	// Header: TITLE and UUID are fused into a single column.
	fmt.Fprintf(w, "   %-16s  %6s  %-*s  %s\n",
		"MTIME", "M",
		findProjMax, "PROJECT",
		"TITLE · UUID")

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
		case active.Confident[h.UUID]:
			marker = "*"
		case active.Guessed[h.UUID]:
			marker = "~"
		}
		title := h.Title
		if title == "" {
			title = "(no title)"
		}
		if active.Confident[h.UUID] {
			if st := active.Status[h.UUID]; st != "" && st != "idle" {
				title = title + "  (" + st + ")"
			}
		}
		// TITLE+UUID fused as "title · uuid" and wraps freely at the
		// title column, bounded only by terminal width.
		fullTitle := title + " · " + h.UUID
		titleLines := wrapSnippet(fullTitle, findTitleStart, 0, termWidth)
		if len(titleLines) == 0 {
			titleLines = []string{fullTitle}
		}
		fmt.Fprintf(w, "%s  %-16s  %6d  %-*s  %s\n",
			marker,
			h.MTime.Format("2006-01-02 15:04"),
			h.MatchCount,
			findProjMax, truncate(shortProject(h.ProjectDir), findProjMax),
			titleLines[0])
		for i := 1; i < len(titleLines); i++ {
			fmt.Fprintf(w, "%*s%s\n", findTitleStart, "", titleLines[i])
		}
		if brief {
			continue
		}
		for _, s := range h.Snippets {
			text := s.Text
			if hl != nil {
				text = hl.ReplaceAllStringFunc(text, highlightMatch)
			}
			// Snippet starts at PROJECT column (col 29) and wraps
			// freely to the terminal edge. Role char at col 27 with
			// a trailing space, so text column 29 is the anchor.
			lines := wrapSnippet(text, findSnippetStart, 0, termWidth)
			rolePrefix := roleChar(s.Role) + " "
			for i, line := range lines {
				if i == 0 {
					fmt.Fprintf(w, "%*s%s%s\n", findRolePrefixCol, "", rolePrefix, line)
				} else {
					fmt.Fprintf(w, "%*s%s\n", findSnippetStart, "", line)
				}
			}
		}
		// Blank line separates sessions when snippets are shown.
		fmt.Fprintln(w)
	}
}

// wrapSnippet breaks text into lines that each fit in the UUID+PROJECT
// "cell": available width = min(endCol - startCol, termWidth - startCol).
// Returns a single-element slice when wrapping is pathological (narrow
// cell). Word-boundary wrap; a word longer than available width is
// emitted on its own line and allowed to overflow.
func wrapSnippet(text string, startCol, endCol, termWidth int) []string {
	// endCol == 0 means "no fixed cell boundary; constrain only by
	// terminal width if known". Used for the TITLE column, which
	// extends to the terminal edge.
	avail := 1 << 20
	if endCol > startCol {
		avail = endCol - startCol
	}
	if termWidth > 0 {
		if w := termWidth - startCol; w < avail {
			avail = w
		}
	}
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

// roleChar maps a message role to a single-character marker used as
// the snippet-row prefix. ">" for user (prompt-arrow direction), "<"
// for assistant (reply direction). Unknown roles get "?".
func roleChar(role string) string {
	switch role {
	case "user":
		return ">"
	case "assistant":
		return "<"
	default:
		return "?"
	}
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
