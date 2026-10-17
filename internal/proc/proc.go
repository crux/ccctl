// Package proc lists running Claude Code processes and resolves which
// session each one has open.
package proc

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/crux/claudectl/internal/project"
)

// Info describes one running claude process and the session it has open.
type Info struct {
	PID         int
	Command     string // basename of the process binary (comm field)
	Cwd         string // process working directory (absolute)
	UUID        string // session UUID the process has open; "" if unknown
	SessionFile string // absolute path to the .jsonl; "" if unknown
}

// ListClaudeProcesses returns one entry per running `claude` process.
//
// Session UUID is resolved in this order:
//
//  1. --resume <uuid> on the command line
//  2. The .jsonl file the process has open (lsof) — authoritative
//  3. Newest .jsonl in the project directory for cwd (last-resort guess)
func ListClaudeProcesses() ([]Info, error) {
	pids, cmds, err := listClaudePIDs()
	if err != nil || len(pids) == 0 {
		return nil, err
	}

	cwds, err := processCwds(pids)
	if err != nil {
		return nil, err
	}
	resume, err := resumeUUIDs(pids)
	if err != nil {
		return nil, err
	}
	openJSONLs, err := openClaudeJSONLs(pids)
	if err != nil {
		return nil, err
	}

	out := make([]Info, 0, len(pids))
	for _, pid := range pids {
		info := Info{PID: pid, Command: cmds[pid], Cwd: cwds[pid], UUID: resume[pid]}

		switch {
		case info.UUID != "":
			info.SessionFile = project.SessionFile(info.Cwd, info.UUID)
		case openJSONLs[pid] != "":
			info.SessionFile = openJSONLs[pid]
			info.UUID = strings.TrimSuffix(filepath.Base(info.SessionFile), ".jsonl")
		case info.Cwd != "":
			if f, uuid := project.NewestSessionFile(info.Cwd); f != "" {
				info.SessionFile, info.UUID = f, uuid
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// listClaudePIDs returns PIDs (and their comm basenames) for processes
// whose comm basename equals "claude". Uses macOS-style ps output.
func listClaudePIDs() ([]int, map[int]string, error) {
	raw, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "comm=").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("ps: %w", err)
	}
	var pids []int
	cmds := map[int]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		base := filepath.Base(fields[1])
		if base != "claude" {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		pids = append(pids, pid)
		cmds[pid] = base
	}
	return pids, cmds, nil
}

// processCwds returns a map of pid → cwd via one lsof call.
func processCwds(pids []int) (map[int]string, error) {
	if len(pids) == 0 {
		return nil, nil
	}
	raw, err := runLsof("-p", joinInts(pids, ","), "-a", "-d", "cwd", "-Fn")
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	var cur int
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			cur, _ = strconv.Atoi(line[1:])
		case 'n':
			if cur != 0 {
				out[cur] = line[1:]
			}
		}
	}
	return out, nil
}

var resumeArgRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// resumeUUIDs returns pid → session uuid, extracted from `--resume <uuid>`
// arguments in each process's command line. PIDs without such an argument
// are absent from the map.
func resumeUUIDs(pids []int) (map[int]string, error) {
	if len(pids) == 0 {
		return nil, nil
	}
	raw, err := exec.Command("ps", "-p", joinInts(pids, ","), "-o", "pid=", "-o", "args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps -p: %w", err)
	}
	out := map[int]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		for i := 1; i < len(fields)-1; i++ {
			if fields[i] == "--resume" && resumeArgRE.MatchString(fields[i+1]) {
				out[pid] = fields[i+1]
				break
			}
		}
	}
	return out, nil
}

// openClaudeJSONLs returns pid → path, naming the first .jsonl under
// ~/.claude/projects/ that each process has open. Authoritative signal
// for which session a process is actually using when --resume wasn't on
// the command line (e.g. launched via `claude --continue` or bare).
func openClaudeJSONLs(pids []int) (map[int]string, error) {
	if len(pids) == 0 {
		return nil, nil
	}
	raw, err := runLsof("-p", joinInts(pids, ","), "-Fn")
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	var cur int
	const marker = "/.claude/projects/"
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			cur, _ = strconv.Atoi(line[1:])
		case 'n':
			name := line[1:]
			if cur == 0 {
				continue
			}
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			if !strings.Contains(name, marker) {
				continue
			}
			if _, already := out[cur]; !already {
				out[cur] = name
			}
		}
	}
	return out, nil
}

// runLsof runs lsof and tolerates its exit code 1 (which it uses to signal
// "no matching files" alongside real errors). Non-ExitError results are
// returned as errors.
func runLsof(args ...string) ([]byte, error) {
	raw, err := exec.Command("lsof", args...).Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return raw, nil
		}
		return nil, fmt.Errorf("lsof: %w", err)
	}
	return raw, nil
}

// IsFileOpen returns true if any process has `path` open.
// Uses lsof; exit code 0 = someone has it open, non-zero = nobody does.
func IsFileOpen(path string) bool {
	return exec.Command("lsof", path).Run() == nil
}

// ActiveUUIDs returns the set of session UUIDs currently open by any
// running claude process. Uses the same 3-tier resolution as
// ListClaudeProcesses (--resume arg → open .jsonl via lsof → newest
// .jsonl in the process's project dir) because any one tier alone misses
// common cases: processes launched with `claude -c` or bare `claude` have
// no --resume, and Claude Code opens-writes-closes its .jsonl so lsof
// usually doesn't see it either.
func ActiveUUIDs() (map[string]bool, error) {
	pids, _, err := listClaudePIDs()
	if err != nil || len(pids) == 0 {
		return nil, err
	}
	resume, err := resumeUUIDs(pids)
	if err != nil {
		return nil, err
	}
	openJSONLs, err := openClaudeJSONLs(pids)
	if err != nil {
		return nil, err
	}
	cwds, err := processCwds(pids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(pids))
	for _, pid := range pids {
		if u := resume[pid]; u != "" {
			out[u] = true
			continue
		}
		if path := openJSONLs[pid]; path != "" {
			out[strings.TrimSuffix(filepath.Base(path), ".jsonl")] = true
			continue
		}
		if cwd := cwds[pid]; cwd != "" {
			if _, uuid := project.NewestSessionFile(cwd); uuid != "" {
				out[uuid] = true
			}
		}
	}
	return out, nil
}

func joinInts(xs []int, sep string) string {
	var b bytes.Buffer
	for i, x := range xs {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(strconv.Itoa(x))
	}
	return b.String()
}
