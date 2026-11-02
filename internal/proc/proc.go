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

	"github.com/crux/ccctl/internal/project"
)

// PIDSession is the per-PID session record Claude Code writes to
// ~/.claude/sessions/<pid>.json. It is the authoritative signal for
// "which session is PID attached to right now" — ps args and open fds
// are both subject to drift (compaction, /clear, etc. can replace the
// session UUID without the process restarting).
type PIDSession struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Version    string `json:"version"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"` // "user" (set by /rename) or "derived"
	Status     string `json:"status"`     // "idle" / "busy" / ""
	Kind       string `json:"kind"`       // "interactive" or "bg" (a background session)
	JobID      string `json:"jobId"`      // background session: the id `claude attach` takes
}

// Title returns the session name when it is a real title. A "derived"
// name is a placeholder Claude Code builds from the directory name
// (e.g. "claude-scripts-42"); it is not a title, so Title returns "".
// A name set by /rename is also in the .jsonl as custom-title, but the
// live record can be newer than the file.
func (p PIDSession) Title() string {
	if p.NameSource == "derived" {
		return ""
	}
	return p.Name
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

// Info describes one running claude process: a session with the session
// it has open, or a helper process of the background-session daemon.
type Info struct {
	PID         int
	PPID        int
	Command     string // Claude Code version (e.g. "2.1.300")
	Cwd         string // process working directory (absolute)
	UUID        string // session UUID the process has open; "" if unknown
	SessionFile string // absolute path to the .jsonl; "" if unknown
	// Title is the session title from PIDSession.Title(); "" if unknown
	// (callers may fall back to session.ReadMeta).
	Title  string
	Status string // "idle" / "busy" / "" (unknown — e.g. legacy path)
	// GuessedUUID is true when UUID came from the newest-in-project-dir
	// fallback; it may be wrong if the project has multiple active
	// sessions. Only set in the legacy path.
	GuessedUUID bool

	Role       Role   // RoleSession, or what a helper process does
	Background bool   // a session that runs under the daemon, not in a terminal
	JobID      string // background session: the id `claude attach` takes
	Args       string // helper process: its command line
	spawnedBy  *spawnedBy
}

// AttachTarget returns the <id|name> of a `claude attach` process, or "".
func (in Info) AttachTarget() string {
	if in.Role != RoleCommand {
		return ""
	}
	return attachTarget(in.Args)
}

// ListSessions returns the running Claude Code sessions, without the
// helper processes of the daemon.
func ListSessions() ([]Info, error) {
	infos, err := ListClaudeProcesses()
	if err != nil {
		return nil, err
	}
	out := infos[:0]
	for _, in := range infos {
		if in.Role == RoleSession {
			out = append(out, in)
		}
	}
	return out, nil
}

// ListClaudeProcesses returns one entry per running claude process:
// every session and every helper process of the daemon (see Role).
//
// A process with a session record (~/.claude/sessions/<pid>.json) is a
// session. A process without one is a helper when its command line says
// so. Otherwise it is a session of an older Claude Code, and its session
// UUID is resolved in this order:
//
//  1. --resume <uuid> on the command line
//  2. Newest .jsonl in the project directory for cwd (last-resort guess)
func ListClaudeProcesses() ([]Info, error) {
	table, err := processTable()
	if err != nil {
		return nil, err
	}
	pids := claudePIDs(table)
	if len(pids) == 0 {
		return nil, nil
	}

	out := make([]Info, 0, len(pids))
	var bare []int
	for _, pid := range pids {
		if ps, err := ReadPIDSession(pid); err == nil && ps.SessionID != "" {
			out = append(out, Info{
				PID:         pid,
				PPID:        table[pid].ppid,
				Command:     ps.Version,
				Cwd:         ps.Cwd,
				UUID:        ps.SessionID,
				SessionFile: project.SessionFile(ps.Cwd, ps.SessionID),
				Title:       ps.Title(),
				Status:      ps.Status,
				Background:  ps.Kind == "bg",
				JobID:       ps.JobID,
			})
			continue
		}
		bare = append(bare, pid)
	}

	if len(bare) > 0 {
		args, err := commandLines(bare)
		if err != nil {
			return nil, err
		}
		var legacy []int
		for _, pid := range bare {
			role := classify(args[pid])
			if role == RoleSession {
				legacy = append(legacy, pid)
				continue
			}
			in := Info{PID: pid, PPID: table[pid].ppid, Role: role, Args: args[pid]}
			if role == RoleDaemon {
				in.spawnedBy = parseSpawnedBy(args[pid])
			}
			out = append(out, in)
		}
		if len(legacy) > 0 {
			l, err := legacyListClaudeProcesses(legacy, table, args)
			if err != nil {
				return nil, err
			}
			out = append(out, l...)
		}
	}

	// A session whose parent is a terminal host runs in the background,
	// also when its record does not say so.
	hosts := map[int]bool{}
	for _, in := range out {
		if in.Role == RoleHost {
			hosts[in.PID] = true
		}
	}
	for i := range out {
		if out[i].Role == RoleSession && hosts[out[i].PPID] {
			out[i].Background = true
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// legacyListClaudeProcesses is the pre-sessions-json resolution path,
// kept as a fallback for older claude builds that didn't write
// ~/.claude/sessions/<pid>.json. Uses --resume arg if present, else
// falls back to newest .jsonl in the process's project dir (a guess).
func legacyListClaudeProcesses(pids []int, table map[int]psRow, args map[int]string) ([]Info, error) {
	cwds, cmds, err := processDetails(pids)
	if err != nil {
		return nil, err
	}

	out := make([]Info, 0, len(pids))
	for _, pid := range pids {
		command := cmds[pid]
		if command == "" {
			command = filepath.Base(table[pid].comm)
		}
		info := Info{PID: pid, PPID: table[pid].ppid, Command: command, Cwd: cwds[pid], UUID: resumeArg(args[pid])}
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

// psRow is one process in the process table.
type psRow struct {
	ppid int
	comm string
}

// processTable returns pid → parent and process name for every process.
// The name can contain spaces: a path with spaces, or a title such as
// "claude bg-pty-host".
func processTable() (map[int]psRow, error) {
	raw, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	table := map[int]psRow{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		table[pid] = psRow{ppid: ppid, comm: strings.Join(fields[2:], " ")}
	}
	return table, nil
}

// claudePIDs returns the PIDs of Claude Code processes, ascending: every
// process with a Claude Code name, and every process with a session
// record that runs a bare version binary (see versionRE).
func claudePIDs(table map[int]psRow) []int {
	records := recordPIDs()
	var pids []int
	for pid, row := range table {
		if isClaudeComm(row.comm) || (records[pid] && looksLikeVersion(row.comm)) {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

// recordPIDs returns the PIDs that have a session record file.
func recordPIDs() map[int]bool {
	out := map[int]bool{}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "sessions"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if pid, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json")); err == nil && strings.HasSuffix(e.Name(), ".json") {
			out[pid] = true
		}
	}
	return out
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

// commandLines returns pid → command line for the given PIDs.
func commandLines(pids []int) (map[int]string, error) {
	// ps exits 1 when a PID ended in between; the other lines still count.
	raw, err := exec.Command("ps", "-p", joinInts(pids, ","), "-o", "pid=", "-o", "args=").Output()
	if err != nil && len(raw) == 0 {
		return map[int]string{}, nil
	}
	out := map[int]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		pidText, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(pidText); err == nil {
			out[pid] = strings.TrimSpace(rest)
		}
	}
	return out, nil
}

// resumeArg returns the session uuid of a `--resume <uuid>` argument in
// a command line, or "".
func resumeArg(args string) string {
	fields := strings.Fields(args)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "--resume" && resumeArgRE.MatchString(fields[i+1]) {
			return fields[i+1]
		}
	}
	return ""
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

// SessionInUse reports whether a running claude process has the session
// uuid (stored at path) open. The session records in ~/.claude/sessions/
// answer this for current Claude Code versions, without lsof. Older
// versions write no records; lsof catches them where it is installed.
func SessionInUse(uuid, path string) bool {
	if ps, err := FindPIDBySessionID(uuid); err == nil && ps != nil {
		return true
	}
	if _, err := exec.LookPath("lsof"); err == nil {
		return IsFileOpen(path)
	}
	return false
}

// IsFileOpen returns true if any process has `path` open.
// Uses lsof; exit code 0 = someone has it open, non-zero = nobody does.
func IsFileOpen(path string) bool {
	return exec.Command("lsof", path).Run() == nil
}

// ActiveSet describes sessions currently open by any running claude
// process, with resolution confidence and per-session status.
//
//	Confident  — UUIDs resolved via PIDSession (authoritative) or
//	             --resume arg (legacy)
//	Guessed    — UUIDs from the newest-in-project-dir fallback;
//	             right when one active session per project, maybe wrong
//	             with several
//	Status     — "idle" / "busy" / "" (unknown; set only when the
//	             PIDSession path resolved the UUID)
//	Background — UUID → the id `claude attach` takes, for sessions that
//	             run in the background
type ActiveSet struct {
	Confident  map[string]bool
	Guessed    map[string]bool
	Status     map[string]string
	Background map[string]string
}

// ActiveUUIDs enumerates currently-open sessions across all running
// claude processes. For modern claude, authoritative via
// ~/.claude/sessions/<pid>.json; legacy fallback uses --resume arg +
// newest-in-dir guess. Helper processes of the daemon do not count.
func ActiveUUIDs() (ActiveSet, error) {
	out := ActiveSet{
		Confident:  map[string]bool{},
		Guessed:    map[string]bool{},
		Status:     map[string]string{},
		Background: map[string]string{},
	}
	infos, err := ListSessions()
	if err != nil {
		return out, err
	}
	for _, in := range infos {
		if in.UUID == "" {
			continue
		}
		if in.GuessedUUID {
			out.Guessed[in.UUID] = true
		} else {
			out.Confident[in.UUID] = true
		}
		if in.Status != "" {
			out.Status[in.UUID] = in.Status
		}
		if in.Background {
			out.Background[in.UUID] = in.AttachID()
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
