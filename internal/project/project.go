// Package project maps between working directories and the local
// Claude Code project namespace under ~/.claude/projects/.
package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
