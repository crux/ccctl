// Package proc lists running Claude Code processes and resolves which
// session each one has open.
package proc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/crux/claudectl/internal/project"
)

// PIDSession is the per-PID session record Claude Code writes to
// ~/.claude/sessions/<pid>.json. It is the authoritative signal for
// "which session is PID attached to right now" — ps args and open fds
// are both subject to drift (compaction, /clear, etc. can replace the
// session UUID without the process restarting).
type PIDSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Version   string `json:"version"`
	Name      string `json:"name"`
	Status    string `json:"status"` // "idle" / "busy" / ""
}

// ReadPIDSession reads ~/.claude/sessions/<pid>.json. Returns a wrapped
// os.ErrNotExist when the file is absent (e.g. older claude builds that
// did not write it, or a PID that has since exited).
func ReadPIDSession(pid int) (*PIDSession, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ps PIDSession
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &ps, nil
}

// FindPIDBySessionID iterates ~/.claude/sessions/*.json and returns the
// first PIDSession whose SessionID matches, filtering out records whose
// PID is no longer running. Returns nil if no live claude process has
// that session open.
func FindPIDBySessionID(sessionID string) (*PIDSession, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".claude", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		ps, err := ReadPIDSession(pid)
		if err != nil || ps == nil || ps.SessionID != sessionID {
			continue
		}
		if !isPIDRunning(pid) {
			continue
		}
		return ps, nil
	}
	return nil, nil
}

// isPIDRunning tests whether a PID is still live. Signal 0 does not
// deliver a signal; it just error-checks the target.
func isPIDRunning(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// Ancestor describes one entry in a process-ancestor chain.
type Ancestor struct {
	PID     int
	PPID    int
	Command string // full argv[0] (basename for display)
}

// PPIDAncestors returns the ancestor chain of pid, starting with pid's
// immediate parent and walking up until a top-level process (ppid <= 1)
// is reached. pid itself is NOT included.
func PPIDAncestors(pid int) ([]Ancestor, error) {
	raw, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	type entry struct {
		ppid int
		comm string
	}
	byPID := make(map[int]entry, 256)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		p, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		pp, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		byPID[p] = entry{pp, strings.Join(fields[2:], " ")}
	}

	e, ok := byPID[pid]
	if !ok {
		return nil, fmt.Errorf("pid %d not found in process table", pid)
	}
	var out []Ancestor
	for e.ppid > 1 {
		parent, ok := byPID[e.ppid]
		if !ok {
			break
		}
		out = append(out, Ancestor{PID: e.ppid, PPID: parent.ppid, Command: parent.comm})
		e = parent
	}
	return out, nil
}

// Info describes one running claude process and the session it has open.
type Info struct {
	PID         int
	Command     string // Claude Code version (e.g. "2.1.300")
	Cwd         string // process working directory (absolute)
	UUID        string // session UUID the process has open; "" if unknown
	SessionFile string // absolute path to the .jsonl; "" if unknown
	Title       string // session display title from PIDSession.Name; "" if
	                   // unknown (callers may fall back to session.ReadMeta)
	Status      string // "idle" / "busy" / "" (unknown — e.g. legacy path)
	GuessedUUID bool   // true when UUID came from the newest-in-project-dir
	                   // fallback; may be wrong if the project has multiple
	                   // active sessions. Only set in the legacy path.
}

// ListClaudeProcesses returns one entry per running `claude` process.
//
// Session UUID is resolved in this order:
//
//  1. --resume <uuid> on the command line
//  2. The .jsonl file the process has open (lsof) — authoritative
//  3. Newest .jsonl in the project directory for cwd (last-resort guess)
func ListClaudeProcesses() ([]Info, error) {
	pids, comms, err := listClaudePIDs()
	if err != nil || len(pids) == 0 {
		return nil, err
	}

	out := make([]Info, 0, len(pids))
	var fallbackPIDs []int
	for _, pid := range pids {
		if ps, err := ReadPIDSession(pid); err == nil && ps.SessionID != "" {
			out = append(out, Info{
				PID:         pid,
				Command:     ps.Version,
				Cwd:         ps.Cwd,
				UUID:        ps.SessionID,
				SessionFile: project.SessionFile(ps.Cwd, ps.SessionID),
				Title:       ps.Name,
				Status:      ps.Status,
			})
			continue
		}
		fallbackPIDs = append(fallbackPIDs, pid)
	}

	if len(fallbackPIDs) > 0 {
		legacy, err := legacyListClaudeProcesses(fallbackPIDs, comms)
		if err != nil {
			return nil, err
		}
		out = append(out, legacy...)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// legacyListClaudeProcesses is the pre-sessions-json resolution path,
// kept as a fallback for older claude builds that didn't write
// ~/.claude/sessions/<pid>.json. Uses --resume arg if present, else
// falls back to newest .jsonl in the process's project dir (a guess).
func legacyListClaudeProcesses(pids []int, comms map[int]string) ([]Info, error) {
	cwds, cmds, err := processDetails(pids)
	if err != nil {
		return nil, err
	}
	resume, err := resumeUUIDs(pids)
	if err != nil {
		return nil, err
	}

	out := make([]Info, 0, len(pids))
	for _, pid := range pids {
		command := cmds[pid]
		if command == "" {
			command = comms[pid]
		}
		info := Info{PID: pid, Command: command, Cwd: cwds[pid], UUID: resume[pid]}
		switch {
		case info.UUID != "":
			info.SessionFile = project.SessionFile(info.Cwd, info.UUID)
		case info.Cwd != "":
			if f, uuid := project.NewestSessionFile(info.Cwd); f != "" {
				info.SessionFile, info.UUID = f, uuid
				info.GuessedUUID = true
			}
		}
		out = append(out, info)
	}
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

// processDetails returns pid → cwd AND pid → command (runtime process title)
// via one lsof call. On macOS, Node processes set the title to a value such
// as "2.1.300" — the Claude Code version — which is more useful for display
// than the comm field ("claude").
func processDetails(pids []int) (cwds, cmds map[int]string, err error) {
	if len(pids) == 0 {
		return nil, nil, nil
	}
	raw, err := runLsof("-p", joinInts(pids, ","), "-a", "-d", "cwd", "-Fcn")
	if err != nil {
		return nil, nil, err
	}
	cwds = map[int]string{}
	cmds = map[int]string{}
	var cur int
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			cur, _ = strconv.Atoi(line[1:])
		case 'c':
			if cur != 0 {
				cmds[cur] = line[1:]
			}
		case 'n':
			if cur != 0 {
				cwds[cur] = line[1:]
			}
		}
	}
	return cwds, cmds, nil
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

// ActiveSet describes sessions currently open by any running claude
// process, with resolution confidence and per-session status.
//
//   Confident — UUIDs resolved via PIDSession (authoritative) or
//               --resume arg (legacy)
//   Guessed   — UUIDs from the newest-in-project-dir fallback;
//               right when one active session per project, maybe wrong
//               with several
//   Status    — "idle" / "busy" / "" (unknown; set only when the
//               PIDSession path resolved the UUID)
type ActiveSet struct {
	Confident map[string]bool
	Guessed   map[string]bool
	Status    map[string]string
}

// ActiveUUIDs enumerates currently-open sessions across all running
// claude processes. For modern claude, authoritative via
// ~/.claude/sessions/<pid>.json; legacy fallback uses --resume arg +
// newest-in-dir guess.
func ActiveUUIDs() (ActiveSet, error) {
	out := ActiveSet{
		Confident: map[string]bool{},
		Guessed:   map[string]bool{},
		Status:    map[string]string{},
	}
	pids, _, err := listClaudePIDs()
	if err != nil || len(pids) == 0 {
		return out, err
	}

	var fallbackPIDs []int
	for _, pid := range pids {
		if ps, err := ReadPIDSession(pid); err == nil && ps.SessionID != "" {
			out.Confident[ps.SessionID] = true
			if ps.Status != "" {
				out.Status[ps.SessionID] = ps.Status
			}
			continue
		}
		fallbackPIDs = append(fallbackPIDs, pid)
	}
	if len(fallbackPIDs) == 0 {
		return out, nil
	}

	resume, err := resumeUUIDs(fallbackPIDs)
	if err != nil {
		return out, err
	}
	cwds, _, err := processDetails(fallbackPIDs)
	if err != nil {
		return out, err
	}
	for _, pid := range fallbackPIDs {
		if u := resume[pid]; u != "" {
			out.Confident[u] = true
			continue
		}
		if cwd := cwds[pid]; cwd != "" {
			if _, uuid := project.NewestSessionFile(cwd); uuid != "" {
				out.Guessed[uuid] = true
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
