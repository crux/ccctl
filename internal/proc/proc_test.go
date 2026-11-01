package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeRecord writes a session record ~/.claude/sessions/<pid>.json.
func writeRecord(t *testing.T, home string, pid int, sessionID string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/x","nameSource":"derived","name":"x-1"}`, pid, sessionID)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := filepath.Join(home, "s.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No records at all: not in use (the file is not open either).
	if SessionInUse("live", file) {
		t.Fatal("in use without any record")
	}

	// A record of a running process (this test) marks the session in use.
	writeRecord(t, home, os.Getpid(), "live")
	if !SessionInUse("live", file) {
		t.Error("record of a running process not seen")
	}

	// A record of a process that no longer runs does not.
	writeRecord(t, home, 999999, "gone")
	if SessionInUse("gone", file) {
		t.Error("record of a stopped process counted as in use")
	}

	// Another session id is not affected.
	if SessionInUse("other", file) {
		t.Error("unrelated session counted as in use")
	}
}

func TestPIDSessionTitle(t *testing.T) {
	cases := []struct {
		ps   PIDSession
		want string
	}{
		{PIDSession{Name: "ccctl-2a", NameSource: "derived"}, ""},
		{PIDSession{Name: "My title", NameSource: "user"}, "My title"},
		{PIDSession{Name: "Old build"}, "Old build"},
	}
	for _, c := range cases {
		if got := c.ps.Title(); got != c.want {
			t.Errorf("%+v: Title() = %q, want %q", c.ps, got, c.want)
		}
	}
}
