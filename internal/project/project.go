// Package project maps between working directories and the local
// Claude Code project namespace under ~/.claude/projects/.
package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crux/ccctl/internal/session"
)

// EncodePath mirrors Claude Code's convention for converting an absolute
// directory path to the subdirectory name under ~/.claude/projects/.
// The characters '/', '.', ' ' and '@' are each replaced by '-'.
func EncodePath(dir string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '.', ' ', '@':
			return '-'
		default:
			return r
		}
	}, dir)
}

// Dir returns the ~/.claude/projects/<encoded> directory for a given
// working directory. Returns "" if the user home cannot be resolved.
func Dir(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", EncodePath(dir))
}

// SessionFile returns the expected .jsonl path for a given cwd + UUID.
func SessionFile(cwd, uuid string) string {
	return filepath.Join(Dir(cwd), uuid+".jsonl")
}

// FindSessionFile searches ~/.claude/projects/* for a <uuid>.jsonl file
// and returns the first match. Returns "" if none is found.
func FindSessionFile(uuid string) (string, error) {
	return findByUUID(uuid, ".jsonl")
}

// FindArchive searches ~/.claude/projects/* for a <uuid>.zip file and
// returns the first match. Returns "" if none is found.
func FindArchive(uuid string) (string, error) {
	return findByUUID(uuid, ".zip")
}

func findByUUID(uuid, ext string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", uuid+ext))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

// tmpPathPrefixes names system-tmp roots whose projects we hide by
// default. Each matches both the decoded cwd ("/tmp/xyz") and its
// Claude-encoded form ("-tmp-xyz").
var tmpPathPrefixes = []string{
	"/private/var/folders",
	"/tmp",
	"/private/tmp",
}

// IsTmp reports whether a project's cwd lives under a system tmp
// directory. Matches both from the decoded cwd and from the encoded
// namespace name (for projects whose cwd we couldn't decode), so a
// drifted or move-not-resumed tmp session is still caught.
func IsTmp(p Info) bool {
	candidate := p.Cwd
	sep := "/"
	prefixes := tmpPathPrefixes
	if candidate == "" {
		candidate = p.EncodedName
		// Encoded names start with '-' (the '/' prefix of the real
		// absolute path) and use '-' instead of '/' everywhere else.
		encoded := make([]string, len(tmpPathPrefixes))
		for i, s := range tmpPathPrefixes {
			encoded[i] = strings.ReplaceAll(s, "/", "-")
		}
		prefixes = encoded
		sep = "-"
	}
	for _, prefix := range prefixes {
		if candidate == prefix || strings.HasPrefix(candidate, prefix+sep) {
			return true
		}
	}
	return false
}

// Info is one project's enumeration record: on-disk directory, basic
// counts, size totals, latest-touched mtime, and the real cwd it
// represents (read from any .jsonl's cwd field; "" when no .jsonl is
// readable, in which case the encoded directory basename is the only
// signal callers have).
type Info struct {
	Dir         string    // absolute path under ~/.claude/projects/
	EncodedName string    // basename of Dir
	Cwd         string    // decoded cwd pulled from first readable .jsonl; may be ""
	Sessions    int       // *.jsonl count
	Archived    int       // *.zip count
	TotalSize   int64     // sum of .jsonl + .zip sizes
	Latest      time.Time // newest mtime among .jsonl + .zip
}

// ListProjects enumerates ~/.claude/projects/ and returns one Info per
// subdirectory. Projects are returned in no particular order (callers
// sort). Enumeration is parallel across projects; each worker does one
// ReadDir and at most one bounded .jsonl read for cwd resolution.
func ListProjects() ([]Info, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	infos := make([]Info, 0, len(entries))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			dir := filepath.Join(root, name)
			info, ok := summariseProject(dir, name)
			if !ok {
				return
			}
			mu.Lock()
			infos = append(infos, info)
			mu.Unlock()
		}(e.Name())
	}
	wg.Wait()
	return infos, nil
}

// summariseProject fills in one Info by scanning the project dir.
// Returns ok=false for empty or unreadable project directories so
// callers can skip them.
//
// Cwd resolution: read session.Meta from the NEWEST .jsonl. ReadMeta
// reverse-scans and takes the last-wins value, so for a session that
// was moved via `ccctl move` and then kept running, the cwd field it
// surfaces is the one claude wrote after the move — not the stale
// cwd from the session's first lines.
func summariseProject(dir, name string) (Info, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Info{}, false
	}
	out := Info{Dir: dir, EncodedName: name}
	var newestJsonl string
	var newestJsonlMtime time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(e.Name(), ".jsonl"):
			out.Sessions++
			out.TotalSize += fi.Size()
			if fi.ModTime().After(newestJsonlMtime) {
				newestJsonl = filepath.Join(dir, e.Name())
				newestJsonlMtime = fi.ModTime()
			}
		case strings.HasSuffix(e.Name(), ".zip"):
			out.Archived++
			out.TotalSize += fi.Size()
		default:
			continue
		}
		if fi.ModTime().After(out.Latest) {
			out.Latest = fi.ModTime()
		}
	}
	if out.Sessions == 0 && out.Archived == 0 {
		return Info{}, false
	}
	// Keep Cwd only when it round-trips to the project namespace
	// name. Protects against two drift cases:
	//   1. Session moved via `ccctl move` but never resumed afterwards —
	//      every cwd in the file still points at the OLD namespace.
	//   2. Session started in cwd A (where its file lives) and then
	//      shifted to cwd B mid-session via --add-dir or shell cd —
	//      last-wins reports B while the file physically lives under A.
	// A cwd whose encoding equals this project's directory basename is
	// the one we can show; anything else means we have no reliable
	// decoded path and fall back to the encoded name at display time.
	if newestJsonl != "" {
		if m, err := session.ReadMeta(newestJsonl); err == nil && m.Cwd != "" {
			if EncodePath(m.Cwd) == name {
				out.Cwd = m.Cwd
			}
		}
	}
	return out, true
}

// NewestSessionFile returns the newest *.jsonl in a cwd's project dir
// along with its UUID (basename without extension). Returns ("","") if
// no .jsonl is present.
func NewestSessionFile(cwd string) (string, string) {
	dir := Dir(cwd)
	if dir == "" {
		return "", ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	type ent struct {
		path string
		mod  int64
	}
	var jsonl []ent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		jsonl = append(jsonl, ent{filepath.Join(dir, e.Name()), info.ModTime().UnixNano()})
	}
	if len(jsonl) == 0 {
		return "", ""
	}
	sort.Slice(jsonl, func(i, j int) bool { return jsonl[i].mod > jsonl[j].mod })
	path := jsonl[0].path
	uuid := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	return path, uuid
}
