package proc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Role says what a claude process does. Claude Code runs background
// sessions (`claude --bg`, or a session sent to the background) under a
// daemon, and the daemon starts helper processes for them:
//
//	claude daemon run --origin transient --spawned-by {…}   the daemon
//	  claude bg-pty-host --bg-pty-host <sock> …              a terminal host
//	    claude bg-spare --bg-spare <sock>                    a session, or a spare
//
// A host is the terminal of exactly one process. That process is a
// background session when it has a session record, and a spare (a
// session started in advance for the next `claude --bg`) when it has
// none. The daemon exits a few seconds after its last background
// session ends.
type Role int

const (
	RoleSession Role = iota // a Claude Code session, in a terminal or in the background
	RoleDaemon              // the daemon that runs the background sessions
	RoleHost                // the terminal host of one background session or spare
	RoleSpare               // a session started in advance for the next `claude --bg`
	RoleCommand             // a claude subcommand, e.g. `claude attach <id>`
)

// String returns the short role name for display; "" for a session.
func (r Role) String() string {
	switch r {
	case RoleDaemon:
		return "daemon"
	case RoleHost:
		return "host"
	case RoleSpare:
		return "spare"
	case RoleCommand:
		return "cmd"
	}
	return ""
}

// subcommands are the claude subcommands that do not run a session
// (from `claude --help`, plus the hidden `daemon`). A process without a
// session record whose first argument is one of them is a RoleCommand.
var subcommands = map[string]bool{
	"agents": true, "attach": true, "auth": true, "auto-mode": true,
	"daemon": true, "doctor": true, "gateway": true, "import": true,
	"install": true, "logs": true, "mcp": true, "plugin": true,
	"plugins": true, "purge": true, "respawn": true, "rm": true,
	"setup-token": true, "stop": true, "kill": true, "ultrareview": true,
	"update": true, "upgrade": true,
}

// classify returns the role of a claude process without a session
// record, from its command line.
func classify(args string) Role {
	f := strings.Fields(args)
	for _, a := range f {
		if a == "--bg-pty-host" {
			return RoleHost
		}
	}
	for _, a := range f {
		if a == "--bg-spare" {
			return RoleSpare
		}
	}
	if len(f) >= 3 && f[1] == "daemon" && f[2] == "run" {
		return RoleDaemon
	}
	if len(f) >= 2 && subcommands[f[1]] {
		return RoleCommand
	}
	return RoleSession
}

// spawnedBy is the `--spawned-by` argument of the daemon: the claude
// process that started it.
type spawnedBy struct {
	Label string `json:"label"`
	Cwd   string `json:"cwd"`
	PID   int    `json:"pid"`
}

// parseSpawnedBy reads the JSON value after `--spawned-by` in a daemon's
// command line. The value can contain spaces (a cwd such as
// "My Drive"), so it is decoded from the raw text, not from fields.
func parseSpawnedBy(args string) *spawnedBy {
	const flag = "--spawned-by "
	i := strings.Index(args, flag)
	if i < 0 {
		return nil
	}
	var sb spawnedBy
	if err := json.NewDecoder(strings.NewReader(args[i+len(flag):])).Decode(&sb); err != nil {
		return nil
	}
	return &sb
}

// attachTarget returns the <id|name> of a `claude attach <id|name>`
// command line, or "".
func attachTarget(args string) string {
	f := strings.Fields(args)
	if len(f) >= 3 && f[1] == "attach" {
		return f[2]
	}
	return ""
}

// Row is one line of the process list: a process, whether it belongs
// to the row above it, and what it does when it is not a session.
type Row struct {
	Info
	Sub  bool   // belongs to the session or daemon above it
	Note string // what the process does; "" for a session
}

// Label returns the role column text: the status for a session ("idle",
// "busy", "bg idle" for a background session), the role otherwise,
// with "└ " in front of a row that belongs to the one above it.
func (r Row) Label() string {
	s := r.Role.String()
	if r.Role == RoleSession {
		s = r.Status
		if r.Background {
			s = strings.TrimSpace("bg " + s)
		}
	}
	if r.Role == RoleCommand && r.Sub && attachTarget(r.Args) != "" {
		s = "attach"
	}
	if r.Sub {
		s = "└ " + s
	}
	return s
}

// Arrange orders processes for display. Sessions come first, by PID.
// Under each background session follow its host and the `claude attach`
// commands that show it. Then each daemon, with its spares under it.
// Anything else comes last.
func Arrange(infos []Info) []Row {
	home, _ := os.UserHomeDir()
	byPID := map[int]Info{}
	for _, in := range infos {
		byPID[in.PID] = in
	}
	sorted := append([]Info(nil), infos...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PID < sorted[j].PID })

	placed := map[int]bool{}
	var rows []Row
	add := func(in Info, sub bool, note string) {
		rows = append(rows, Row{Info: in, Sub: sub, Note: note})
		placed[in.PID] = true
	}

	for _, s := range sorted {
		if s.Role != RoleSession {
			continue
		}
		add(s, false, "")
		if !s.Background {
			continue
		}
		if h, ok := byPID[s.PPID]; ok && h.Role == RoleHost {
			add(h, true, fmt.Sprintf("terminal of %d", s.PID))
		}
		for _, c := range sorted {
			if c.Role == RoleCommand && !placed[c.PID] && s.Attaches(attachTarget(c.Args)) {
				add(c, true, "shows it in a terminal")
			}
		}
	}

	for _, d := range sorted {
		if d.Role != RoleDaemon {
			continue
		}
		add(d, false, daemonNote(d.spawnedBy, home))
		for _, h := range sorted {
			if h.Role != RoleHost || placed[h.PID] || h.PPID != d.PID {
				continue
			}
			spare := 0
			for _, c := range sorted {
				if c.Role == RoleSpare && c.PPID == h.PID {
					spare = c.PID
				}
			}
			if spare == 0 {
				add(h, true, "terminal host, starting")
				continue
			}
			add(byPID[spare], true, fmt.Sprintf("kept ready for the next background session (host %d)", h.PID))
			placed[h.PID] = true
		}
	}

	for _, x := range sorted {
		if placed[x.PID] {
			continue
		}
		var note string
		switch x.Role {
		case RoleHost:
			note = "terminal host of a background session"
		case RoleSpare:
			note = "kept ready for the next background session"
		case RoleCommand:
			note = tildeHome(x.Args, home)
		}
		add(x, false, note)
	}
	return rows
}

// Attaches reports whether `claude attach <target>` opens this
// background session. Claude Code takes the short id or a part of the
// session name.
func (in Info) Attaches(target string) bool {
	if target == "" || !in.Background {
		return false
	}
	if target == in.JobID || strings.HasPrefix(in.UUID, target) {
		return true
	}
	return in.Title != "" && strings.Contains(strings.ToLower(in.Title), strings.ToLower(target))
}

// AttachID returns the id that `claude attach` takes for a background
// session: the jobId of its record. Without one it falls back to the
// first 8 characters of the session id, which is what every jobId seen
// so far was.
func (in Info) AttachID() string {
	if in.JobID != "" {
		return in.JobID
	}
	if len(in.UUID) >= 8 {
		return in.UUID[:8]
	}
	return in.UUID
}

func daemonNote(sb *spawnedBy, home string) string {
	if sb == nil {
		return "runs the background sessions"
	}
	note := "started by"
	if sb.Label != "" {
		note += " " + sb.Label
	}
	if sb.Cwd != "" {
		note += " in " + filepath.Base(sb.Cwd)
	}
	if sb.PID > 0 {
		state := ""
		if !isPIDRunning(sb.PID) {
			state = ", ended"
		}
		note += fmt.Sprintf(" (pid %d%s)", sb.PID, state)
	}
	return note
}

func tildeHome(s, home string) string {
	if home == "" {
		return s
	}
	return strings.ReplaceAll(s, home, "~")
}

// versionRE matches the file name of a Claude Code version binary
// (~/.local/share/claude/versions/2.1.316). A session that Claude Code
// moves into the background runs that binary directly, so its process
// name is the version, not "claude".
var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+`)

// isClaudeComm reports whether a process name belongs to Claude Code.
// Helper processes set their title, so the name can carry words after
// the program ("claude bg-pty-host").
func isClaudeComm(comm string) bool {
	f := strings.Fields(comm)
	if len(f) == 0 {
		return false
	}
	return filepath.Base(f[0]) == "claude" || strings.Contains(f[0], "/claude/versions/")
}

// looksLikeVersion reports whether a process name is a bare version
// binary. Only a process with a session record counts on that basis.
func looksLikeVersion(comm string) bool {
	f := strings.Fields(comm)
	return len(f) > 0 && versionRE.MatchString(filepath.Base(f[0]))
}
