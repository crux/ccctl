// Package search implements content-level search across Claude Code
// session .jsonl files. Phase 1: full scan on every call (bounded
// parallelism, two-pass per file: byte-level pre-filter then
// stream-parse). Phase 2 (deferred): persistent index.
//
// Invariant worth preserving for the eventual index: session .jsonl
// files and ~/.claude/history.jsonl are strictly append-only. A frozen
// (closed) session's bytes never change; a live session only grows at
// the tail. So an incremental index stores (last_indexed_offset,
// mtime_at_index) per file, and catch-up is a seek-to-offset + read-tail
// operation — no daemon required, a lazy "catch up on each find call"
// is probably enough. Note this when implementing phase 2.
package search

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hit describes one session that matched the search pattern.
type Hit struct {
	UUID       string
	MTime      time.Time
	Path       string    // absolute path to the matching .jsonl
	ProjectDir string    // absolute path (realpath) of the session's project
	Title      string    // display title; "" if unknown
	MatchCount int       // total number of pattern occurrences in user/assistant text
	Snippets   []Snippet // oldest-first; capped by Opts.LastN
}

// Snippet captures one matched occurrence with surrounding context.
type Snippet struct {
	Role string // "user" or "assistant"
	Text string // whitespace-normalised context around the match, ellipsised
}

// Opts tunes a Search call.
type Opts struct {
	Pattern       string // required
	CaseSensitive bool   // default is case-insensitive
	ProjectDir    string // if "", searches all projects (~/.claude/projects/*)
	LastN         int    // snippets kept per session, oldest-first; <=0 means 1
	Workers       int    // parallel file scanners; <=0 means a sensible default (16)
}

const (
	defaultWorkers    = 16
	snippetBefore     = 40
	snippetAfter      = 40
	snippetMaxContext = snippetBefore + snippetAfter + 32 // + pattern headroom
)

// Search enumerates session .jsonl files in the configured scope,
// matches the pattern against user + assistant text, and returns hits
// sorted by MTime descending. Returns nil (and no error) if no sessions
// match.
func Search(opts Opts) ([]Hit, error) {
	if opts.Pattern == "" {
		return nil, fmt.Errorf("search: empty pattern")
	}
	if opts.LastN < 1 {
		opts.LastN = 1
	}
	if opts.Workers < 1 {
		opts.Workers = defaultWorkers
	}

	paths, err := enumerateSessionPaths(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}

	flags := "(?i)"
	if opts.CaseSensitive {
		flags = ""
	}
	re, err := regexp.Compile(flags + regexp.QuoteMeta(opts.Pattern))
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}

	type result struct {
		hit *Hit
		err error
	}
	results := make(chan result, len(paths))
	sem := make(chan struct{}, opts.Workers)
	var wg sync.WaitGroup

	for _, p := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			hit, err := scanFile(path, re, opts)
			results <- result{hit, err}
		}(p)
	}
	go func() { wg.Wait(); close(results) }()

	hits := make([]Hit, 0)
	var firstErr error
	for r := range results {
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		if r.hit != nil {
			hits = append(hits, *r.hit)
		}
	}
	if firstErr != nil && len(hits) == 0 {
		return nil, firstErr
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].MTime.After(hits[j].MTime) })
	return hits, nil
}

// enumerateSessionPaths returns the set of .jsonl paths to scan.
func enumerateSessionPaths(projectDir string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".claude", "projects")
	var dirs []string
	if projectDir != "" {
		dirs = []string{projectDir}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
	}
	var out []string
	for _, d := range dirs {
		matches, err := filepath.Glob(filepath.Join(d, "*.jsonl"))
		if err != nil {
			continue
		}
		out = append(out, matches...)
	}
	return out, nil
}

// scanFile runs the two-pass search on one .jsonl: a byte-level
// regexp.Match pre-filter, then a line-by-line pass to count matches
// in user/assistant text and capture snippets. Returns (nil, nil) if
// the file contains no matches in the targeted content.
func scanFile(path string, re *regexp.Regexp, opts Opts) (*Hit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !re.Match(data) {
		return nil, nil
	}

	hit := &Hit{
		UUID:       strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:       path,
		ProjectDir: resolveProjectCwd(filepath.Dir(path), data),
	}
	if fi, err := os.Stat(path); err == nil {
		hit.MTime = fi.ModTime()
	}

	// Ring buffer of the last N snippets (chronological order of
	// appearance). We overwrite oldest once we exceed capacity.
	snippets := make([]Snippet, 0, opts.LastN)

	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if !re.Match(line) {
			continue
		}
		var entry struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			Message     *struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		// Keep the latest custom-title we see (sessions.go uses the
		// JSON side-car; here we fall back to .jsonl for titles the
		// side-car doesn't have, e.g. closed sessions).
		if entry.Type == "custom-title" && entry.CustomTitle != "" {
			hit.Title = entry.CustomTitle
		}
		if entry.Message == nil {
			continue
		}
		if entry.Message.Role != "user" && entry.Message.Role != "assistant" {
			continue
		}
		texts := extractText(entry.Message.Content)
		for _, text := range texts {
			locs := re.FindAllStringIndex(text, -1)
			if len(locs) == 0 {
				continue
			}
			hit.MatchCount += len(locs)
			// Grab a snippet from the LAST match in this text block;
			// append to the ring. The outer traversal is already in
			// file (chronological) order, so appending preserves order.
			last := locs[len(locs)-1]
			snippets = appendRing(snippets, Snippet{
				Role: entry.Message.Role,
				Text: makeSnippet(text, last[0], last[1]),
			}, opts.LastN)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if hit.MatchCount == 0 {
		// Pre-filter matched (string occurs in bytes), but all
		// occurrences fell outside user/assistant content — skip.
		return nil, nil
	}
	hit.Snippets = snippets
	return hit, nil
}

// extractText pulls plain-text content from a message.content payload.
// Claude Code uses two shapes: a bare string (older) or an array of
// typed blocks (newer, {"type":"text","text":"..."} among others).
// Returns one string per text block.
func extractText(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	// Try array of blocks first.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		out := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				out = append(out, b.Text)
			}
		}
		return out
	}
	// Fall back to bare string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return []string{s}
	}
	return nil
}

// appendRing appends a snippet to a chronological ring buffer capped at
// capN entries, dropping the oldest when full.
func appendRing(ring []Snippet, s Snippet, capN int) []Snippet {
	if len(ring) < capN {
		return append(ring, s)
	}
	// shift left by one; append s at the end
	copy(ring, ring[1:])
	ring[len(ring)-1] = s
	return ring
}

// makeSnippet builds a display-ready context string around a match.
// Collapses runs of whitespace to single spaces and ellipsises both
// sides if content extended beyond the window.
func makeSnippet(text string, matchStart, matchEnd int) string {
	start := matchStart - snippetBefore
	end := matchEnd + snippetAfter
	leftTrunc := start > 0
	rightTrunc := end < len(text)
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	ctx := text[start:end]
	ctx = whitespaceRE.ReplaceAllString(ctx, " ")
	ctx = strings.TrimSpace(ctx)
	if leftTrunc {
		ctx = "…" + ctx
	}
	if rightTrunc {
		ctx = ctx + "…"
	}
	return ctx
}

var whitespaceRE = regexp.MustCompile(`\s+`)

// resolveProjectCwd tries to pull the cwd field from any .jsonl entry
// that has one. Scans ALL lines — Claude Code puts cwd on various event
// types and not always near the top. Cheap byte-level pre-filter keeps
// cost down. Returns "" when the file carries no cwd anywhere; caller
// may enrich via PIDSession or other sources.
func resolveProjectCwd(projectEncodedDir string, fileData []byte) string {
	cwdMarker := []byte(`"cwd":`)
	scanner := bufio.NewScanner(bytes.NewReader(fileData))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, cwdMarker) {
			continue
		}
		var entry struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(line, &entry); err == nil && entry.Cwd != "" {
			return entry.Cwd
		}
	}
	return "" // caller may enrich
}
