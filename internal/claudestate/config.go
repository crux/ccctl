package claudestate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// KeyCopy is one project entry of ~/.claude.json, keyed by directory.
// Such an entry holds the trust decision, allowed tools, MCP servers
// and the stats of the last session for that directory.
type KeyCopy struct {
	From string
	To   string
	Kept bool // To has an entry already; it stays and From is not copied
}

// PlanConfig lists the project entries that CopyConfig would copy. It
// reads ~/.claude.json without the lock and writes nothing.
func PlanConfig(r Rebase) ([]KeyCopy, error) {
	data, err := os.ReadFile(ConfigPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, _, projects, err := splitConfig(data)
	if err != nil {
		return nil, err
	}
	return planCopies(projects, r), nil
}

// CopyConfig copies each project entry under an old directory to the
// matching key under the new directory. It works under Claude Code's
// config lock and writes the file atomically. The old entries stay, so
// a lost write can never lose settings. After the write, CopyConfig
// reads the file again and reports an error when a copied key is
// missing.
func CopyConfig(r Rebase) ([]KeyCopy, error) {
	path := ConfigPath()
	real, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	copies, err := copyConfigLocked(path, real, r)
	if err != nil || len(copies) == 0 {
		return copies, err
	}

	data, err := os.ReadFile(real)
	if err != nil {
		return copies, fmt.Errorf("check after write: %w", err)
	}
	_, _, projects, err := splitConfig(data)
	if err != nil {
		return copies, fmt.Errorf("check after write: %w", err)
	}
	present := map[string]bool{}
	for _, m := range projects {
		present[m.Key] = true
	}
	for _, c := range copies {
		if !c.Kept && !present[c.To] {
			return copies, fmt.Errorf("entry %s is missing after the write", c.To)
		}
	}
	return copies, nil
}

func copyConfigLocked(lockPath, real string, r Rebase) ([]KeyCopy, error) {
	unlock, err := lockFile(lockPath)
	if err != nil {
		return nil, err
	}
	defer unlock()

	fi, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return nil, err
	}
	top, idx, projects, err := splitConfig(data)
	if err != nil {
		return nil, err
	}
	copies := planCopies(projects, r)

	values := make(map[string]json.RawMessage, len(projects))
	for _, m := range projects {
		values[m.Key] = m.Value
	}
	added := 0
	for _, c := range copies {
		if c.Kept {
			continue
		}
		projects = append(projects, member{Key: c.To, Value: values[c.From]})
		added++
	}
	if added == 0 {
		return copies, nil
	}

	enc, err := encodeObject(projects)
	if err != nil {
		return nil, err
	}
	top[idx].Value = enc
	out, err := encodeObjectIndented(top)
	if err != nil {
		return nil, err
	}
	if bytes.HasSuffix(data, []byte("\n")) {
		out = append(out, '\n')
	}
	if err := writeAtomic(real, out, fi.Mode().Perm()); err != nil {
		return nil, err
	}
	return copies, nil
}

// splitConfig parses ~/.claude.json into its top-level members, the
// index of "projects" among them and the members of "projects". A file
// without "projects" yields idx -1 and no project members.
func splitConfig(data []byte) (top []member, idx int, projects []member, err error) {
	top, err = parseObject(data)
	if err != nil {
		return nil, -1, nil, fmt.Errorf("parse ~/.claude.json: %w", err)
	}
	for i, m := range top {
		if m.Key == "projects" {
			projects, err = parseObject(m.Value)
			if err != nil {
				return nil, -1, nil, fmt.Errorf("parse ~/.claude.json projects: %w", err)
			}
			return top, i, projects, nil
		}
	}
	return top, -1, nil, nil
}

// planCopies maps each affected key. A target that exists already, or
// that an earlier key in the file claims, is marked Kept.
func planCopies(projects []member, r Rebase) []KeyCopy {
	taken := make(map[string]bool, len(projects))
	for _, m := range projects {
		taken[m.Key] = true
	}
	var out []KeyCopy
	for _, m := range projects {
		to, ok := r.Map(m.Key)
		if !ok {
			continue
		}
		out = append(out, KeyCopy{From: m.Key, To: to, Kept: taken[to]})
		taken[to] = true
	}
	return out
}

// writeAtomic writes data to a temp file next to path and renames it
// over path, so a reader never sees a partial file.
func writeAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp := path + ".ccctl-" + strconv.Itoa(os.Getpid()) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
