package claudestate

import (
	"os"
	"path/filepath"
	"strings"
)

// Rebase maps paths under an old directory to the same relative path
// under a new directory. Old can hold several spellings of the same
// directory, for example the path as typed and its symlink-free form.
type Rebase struct {
	Old []string
	New string
}

// Map returns the new path for p and true when p is one of the old
// directories or lies below one of them.
func (r Rebase) Map(p string) (string, bool) {
	for _, old := range r.Old {
		if p == old {
			return r.New, true
		}
		if strings.HasPrefix(p, old+"/") {
			return r.New + p[len(old):], true
		}
	}
	return "", false
}

// ConfigPath returns the path of ~/.claude.json.
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// HistoryPath returns the path of ~/.claude/history.jsonl.
func HistoryPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "history.jsonl")
}
