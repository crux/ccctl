// Package claudestate edits Claude Code's global state files while
// Claude Code runs: ~/.claude.json (per-project settings) and
// ~/.claude/history.jsonl (prompt history).
//
// Claude Code guards both files with proper-lockfile: the lock is a
// directory "<file>.lock", created with mkdir and treated as abandoned
// after 10 s. Under the config lock, Claude Code reads the file again,
// applies its change and writes the result atomically. A write by ccctl
// under the same lock is therefore kept by every running claude.
package claudestate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// staleAfter matches proper-lockfile's default `stale` option, which
// Claude Code uses for both files.
const staleAfter = 10 * time.Second

// lockWait is how long ccctl waits for a lock that another process
// holds. Claude Code holds its locks for milliseconds. A variable so
// tests can shorten it.
var lockWait = 5 * time.Second

// lockFile acquires "<file>.lock" with the proper-lockfile protocol and
// returns the release function. A lock directory older than staleAfter
// is removed, as proper-lockfile does.
func lockFile(file string) (func(), error) {
	lockDir := file + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(lockDir, 0o777)
		if err == nil {
			return func() { _ = os.Remove(lockDir) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock %s: %w", lockDir, err)
		}
		if fi, serr := os.Stat(lockDir); serr == nil && time.Since(fi.ModTime()) > staleAfter {
			_ = os.Remove(lockDir)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s is held by another process; try again", lockDir)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
