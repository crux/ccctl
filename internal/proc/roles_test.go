package proc

import (
	"strconv"
	"strings"
	"testing"
)

// Command lines as Claude Code 2.1.316 shows them in `ps -o args=`.
const (
	argsDaemon    = `/Users/u/.local/bin/claude daemon run --origin transient --spawned-by {"label":"claude --bg","cwd":"/Users/u/My Drive/ccctl","pid":11115}`
	argsSpareHost = `claude bg-pty-host --bg-pty-host /tmp/cc-daemon-501/23b92326/spare/8abbc044.pty.sock 200 50 -- /Users/u/.local/share/claude/versions/2.1.316 --bg-spare /tmp/cc-daemon-501/23b92326/spare/8abbc044.claim.sock`
	argsSpare     = `claude bg-spare --bg-spare /tmp/cc-daemon-501/23b92326/spare/8abbc044.claim.sock`
	argsHost      = `claude --bg-pty-host /tmp/cc-daemon-501/23b92326/pty/7fc02bcf.sock 200 50 -- /Users/u/.local/share/claude/versions/2.1.316 --session-id 7fc02bcf-757a-468e-a019-665cced1c7d0 --fork-session --resume /Users/u/.claude/projects/-x/297efdf1-0000-0000-0000-000000000000.jsonl`
	argsAttach    = `claude attach ee4c0e7d`
)

func TestClassify(t *testing.T) {
	cases := []struct {
		args string
		want Role
	}{
		{argsDaemon, RoleDaemon},
		{argsSpareHost, RoleHost},
		{argsHost, RoleHost},
		{argsSpare, RoleSpare},
		{argsAttach, RoleCommand},
		{"claude mcp serve", RoleCommand},
		{"claude", RoleSession},
		{"claude --resume 297efdf1-0000-0000-0000-000000000000", RoleSession},
		{`claude "fix the daemon bug"`, RoleSession},
		{"", RoleSession},
	}
	for _, c := range cases {
		if got := classify(c.args); got != c.want {
			t.Errorf("classify(%.40q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestParseSpawnedBy(t *testing.T) {
	sb := parseSpawnedBy(argsDaemon)
	if sb == nil {
		t.Fatal("no spawned-by found")
	}
	if sb.Label != "claude --bg" || sb.Cwd != "/Users/u/My Drive/ccctl" || sb.PID != 11115 {
		t.Errorf("got %+v", *sb)
	}
	if parseSpawnedBy("claude daemon run") != nil {
		t.Error("spawned-by found where there is none")
	}
	if parseSpawnedBy("claude daemon run --spawned-by {broken") != nil {
		t.Error("broken JSON accepted")
	}
}

func TestClaudeComm(t *testing.T) {
	cases := []struct {
		comm          string
		claude, vbare bool
	}{
		{"claude", true, false},
		{"/Users/u/.local/bin/claude", true, false},
		{"claude bg-pty-host", true, false},
		{"/Users/u/.local/share/claude/versions/2.1.316", true, true},
		{"2.1.316", false, true},
		{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", false, false},
		{"zsh", false, false},
	}
	for _, c := range cases {
		if got := isClaudeComm(c.comm); got != c.claude {
			t.Errorf("isClaudeComm(%q) = %v", c.comm, got)
		}
		if got := looksLikeVersion(c.comm); got != c.vbare {
			t.Errorf("looksLikeVersion(%q) = %v", c.comm, got)
		}
	}
}

func TestResumeArg(t *testing.T) {
	if got := resumeArg("claude --resume 297efdf1-0000-0000-0000-000000000000 --x"); got != "297efdf1-0000-0000-0000-000000000000" {
		t.Errorf("got %q", got)
	}
	// A path after --resume is not a session id.
	if got := resumeArg(argsHost); got != "" {
		t.Errorf("path taken as session id: %q", got)
	}
}

// world is a daemon with one background session (attached in a
// terminal), one spare, and an interactive session.
func world() []Info {
	return []Info{
		// PID 99999999 is above every PID limit, so it never runs.
		{PID: 300, PPID: 1, Role: RoleDaemon, Args: argsDaemon, spawnedBy: &spawnedBy{Label: "claude --bg", Cwd: "/Users/u/My Drive/ccctl", PID: 99999999}},
		{PID: 310, PPID: 300, Role: RoleHost, Args: argsHost},
		{PID: 311, PPID: 310, UUID: "7fc02bcf-757a-468e-a019-665cced1c7d0", Title: "outlook-scan", Status: "idle", Background: true, JobID: "7fc02bcf"},
		{PID: 320, PPID: 300, Role: RoleHost, Args: argsSpareHost},
		{PID: 321, PPID: 320, Role: RoleSpare, Args: argsSpare},
		{PID: 200, PPID: 50, UUID: "d7436901-3585-48cc-9d00-8db253cca50c", Title: "DIGT", Status: "busy"},
		{PID: 400, PPID: 60, Role: RoleCommand, Args: "claude attach outlook"},
		{PID: 401, PPID: 61, Role: RoleCommand, Args: "claude mcp serve"},
	}
}

func TestArrange(t *testing.T) {
	rows := Arrange(world())
	var got []string
	for _, r := range rows {
		got = append(got, strings.TrimSpace(r.Label()+" "+strconv.Itoa(r.PID)+" "+r.Note))
	}
	want := []string{
		"busy 200",
		"bg idle 311",
		"└ host 310 terminal of 311",
		"└ attach 400 shows it in a terminal",
		"daemon 300 started by claude --bg in ccctl (pid 99999999, ended)",
		"└ spare 321 kept ready for the next background session (host 320)",
		"cmd 401 claude mcp serve",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Arrange:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestArrangeKeepsEveryProcess(t *testing.T) {
	in := world()
	// An orphaned host and spare (daemon gone) still show up.
	in = append(in, Info{PID: 500, PPID: 1, Role: RoleHost, Args: argsSpareHost})
	rows := Arrange(in)
	if len(rows) != len(in)-1 { // the spare host 320 folds into its spare's row
		t.Errorf("%d rows for %d processes", len(rows), len(in))
	}
	seen := map[int]bool{}
	for _, r := range rows {
		if seen[r.PID] {
			t.Errorf("pid %d listed twice", r.PID)
		}
		seen[r.PID] = true
	}
	if !seen[500] {
		t.Error("orphaned host missing")
	}
}

func TestAttaches(t *testing.T) {
	s := Info{UUID: "7fc02bcf-757a-468e-a019-665cced1c7d0", Title: "outlook-scan", Background: true, JobID: "7fc02bcf"}
	for _, target := range []string{"7fc02bcf", "7fc0", "outlook", "SCAN"} {
		if !s.Attaches(target) {
			t.Errorf("Attaches(%q) = false", target)
		}
	}
	for _, target := range []string{"", "ee4c0e7d", "ccctl"} {
		if s.Attaches(target) {
			t.Errorf("Attaches(%q) = true", target)
		}
	}
	s.Background = false
	if s.Attaches("7fc02bcf") {
		t.Error("an interactive session cannot be attached")
	}
}

func TestAttachID(t *testing.T) {
	if got := (Info{UUID: "7fc02bcf-757a", JobID: "job1"}).AttachID(); got != "job1" {
		t.Errorf("jobId not used: %q", got)
	}
	if got := (Info{UUID: "7fc02bcf-757a"}).AttachID(); got != "7fc02bcf" {
		t.Errorf("fallback: %q", got)
	}
}
