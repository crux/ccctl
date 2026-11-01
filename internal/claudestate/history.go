package claudestate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Claude Code shows up-arrow prompt history per project: it keeps the
// entries of history.jsonl whose "project" equals the current project
// directory. After a directory move, the new path has no entries.
//
// MoveHistory rewrites "project" in the affected entries. Claude Code
// appends to history.jsonl under the history lock, and its own
// retention prune rewrites the file under that lock. A rewrite by
// ccctl under the same lock therefore loses no line.

// PlanHistory counts the entries that MoveHistory would change. It
// reads the file without the lock and writes nothing.
func PlanHistory(r Rebase) (int, error) {
	data, err := os.ReadFile(HistoryPath())
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	_, n, err := rewriteHistory(data, r)
	return n, err
}

// MoveHistory sets "project" to the new path in each history entry
// under an old directory. It works under Claude Code's history lock and
// replaces the file atomically. All other bytes stay as they are. A
// second run changes nothing.
func MoveHistory(r Rebase) (int, error) {
	path := HistoryPath()
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file; not rewritten", path)
	}

	unlock, err := lockFile(path)
	if err != nil {
		return 0, err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	out, n, err := rewriteHistory(data, r)
	if err != nil || n == 0 {
		return 0, err
	}
	if err := writeAtomic(path, out, fi.Mode().Perm()); err != nil {
		return 0, err
	}
	return n, nil
}

// rewriteHistory returns data with "project" rebased in every matching
// line, and the number of changed lines. Lines are parsed only when
// their bytes contain an old path, which keeps the lock time short.
func rewriteHistory(data []byte, r Rebase) ([]byte, int, error) {
	needles := make([][]byte, 0, len(r.Old))
	for _, old := range r.Old {
		s, err := marshalString(old)
		if err != nil {
			return nil, 0, err
		}
		needles = append(needles, append([]byte(`"project":`), s[:len(s)-1]...))
	}

	out := make([]byte, 0, len(data))
	n := 0
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			end = len(data)
		} else {
			end++
		}
		line := data[:end]
		data = data[end:]

		body := bytes.TrimRight(line, "\r\n")
		if !containsAny(body, needles) {
			out = append(out, line...)
			continue
		}
		var e struct {
			Project string `json:"project"`
		}
		np, ok := "", false
		if json.Unmarshal(body, &e) == nil {
			np, ok = r.Map(e.Project)
		}
		if !ok {
			out = append(out, line...)
			continue
		}
		moved, err := withProject(body, np)
		if err != nil {
			out = append(out, line...)
			continue
		}
		out = append(out, moved...)
		out = append(out, line[len(body):]...)
		n++
	}
	return out, n, nil
}

func containsAny(b []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(b, n) {
			return true
		}
	}
	return false
}

// withProject returns line with its "project" value replaced. All
// other members keep their order and bytes.
func withProject(line []byte, project string) ([]byte, error) {
	members, err := parseObject(line)
	if err != nil {
		return nil, err
	}
	v, err := marshalString(project)
	if err != nil {
		return nil, err
	}
	for i := range members {
		if members[i].Key == "project" {
			members[i].Value = v
		}
	}
	return encodeObject(members)
}
