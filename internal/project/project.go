// Package project maps between working directories and the local
// Claude Code project namespace under ~/.claude/projects/.
package project

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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

// cwdScanLimit caps how far we read into a .jsonl looking for a cwd
// field. cwd typically appears on user turns within the first few KB,
// so 1 MB is a wide safety margin without opening a window into
// multi-hundred-MB files.
const cwdScanLimit int64 = 1024 * 1024

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
func summariseProject(dir, name string) (Info, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Info{}, false
	}
	out := Info{Dir: dir, EncodedName: name}
	var firstJsonl string
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
			if firstJsonl == "" {
				firstJsonl = filepath.Join(dir, e.Name())
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
	if firstJsonl != "" {
		out.Cwd = readCwdFrom(firstJsonl)
	}
	return out, true
}

// readCwdFrom scans the opening cwdScanLimit bytes of a .jsonl for any
// entry carrying a cwd field. Returns "" when none is found or the
// file cannot be opened.
func readCwdFrom(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	reader := bufio.NewReader(&io.LimitedReader{R: f, N: cwdScanLimit})
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 128*1024), 1024*1024)
	marker := []byte(`"cwd":`)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, marker) {
			continue
		}
		var entry struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(line, &entry); err == nil && entry.Cwd != "" {
			return entry.Cwd
		}
	}
	return ""
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
