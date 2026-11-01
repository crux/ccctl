package claudestate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeFormat is a sample in the layout JSON.stringify(x, null, 2)
// produces, with the value kinds ~/.claude.json holds.
const claudeFormat = `{
  "numStartups": 1234,
  "ratio": 1.5e-7,
  "tips": {},
  "list": [],
  "html": "<a & b>",
  "quote": "say \"hi\"\n",
  "umlaut": "Lüsebrink",
  "projects": {
    "/a/b": {
      "allowedTools": [
        "Bash(ls:*)"
      ],
      "hasTrustDialogAccepted": true
    }
  }
}`

func TestRoundTripClaudeFormat(t *testing.T) {
	top, err := parseObject([]byte(claudeFormat))
	if err != nil {
		t.Fatal(err)
	}
	out, err := encodeObjectIndented(top)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != claudeFormat {
		t.Fatalf("round trip changed the bytes:\n%s", out)
	}
}

func TestRoundTripCompactLine(t *testing.T) {
	line := `{"display":"fix <b> & \"c\"","pastedContents":{},"timestamp":1730000000000,"project":"/a/b","sessionId":"x"}`
	out, err := withProject([]byte(line), "/x/b")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(line, `"/a/b"`, `"/x/b"`, 1)
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

func TestRebaseMap(t *testing.T) {
	r := Rebase{Old: []string{"/a/b", "/link/b"}, New: "/x/b"}
	cases := map[string]string{
		"/a/b":       "/x/b",
		"/a/b/c/d":   "/x/b/c/d",
		"/link/b/c":  "/x/b/c",
		"/a/bc":      "",
		"/a":         "",
		"/a/b-other": "",
	}
	for in, want := range cases {
		got, ok := r.Map(in)
		if want == "" {
			if ok {
				t.Errorf("Map(%q) = %q, want no match", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("Map(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCopyConfig(t *testing.T) {
	home := setHome(t)
	cfg := `{
  "numStartups": 3,
  "projects": {
    "/a/b": {
      "hasTrustDialogAccepted": true
    },
    "/a/b/sub": {
      "allowedTools": [
        "Read"
      ]
    },
    "/a/bc": {
      "hasTrustDialogAccepted": false
    },
    "/x/b/sub": {
      "lastCost": 1
    }
  },
  "after": "kept"
}
`
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Rebase{Old: []string{"/a/b"}, New: "/x/b"}
	plan, err := PlanConfig(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || plan[0].To != "/x/b" || plan[0].Kept || plan[1].To != "/x/b/sub" || !plan[1].Kept {
		t.Fatalf("plan = %+v", plan)
	}

	copies, err := CopyConfig(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(copies) != 2 {
		t.Fatalf("copies = %+v", copies)
	}

	data, _ := os.ReadFile(path)
	var got struct {
		Projects map[string]map[string]any `json:"projects"`
		After    string                    `json:"after"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Projects["/x/b"]["hasTrustDialogAccepted"] != true {
		t.Errorf("/x/b not copied: %v", got.Projects["/x/b"])
	}
	if got.Projects["/x/b/sub"]["lastCost"] != float64(1) {
		t.Errorf("existing /x/b/sub was overwritten: %v", got.Projects["/x/b/sub"])
	}
	if _, ok := got.Projects["/a/b"]; !ok {
		t.Error("old entry /a/b was removed")
	}
	if _, ok := got.Projects["/x/bc"]; ok {
		t.Error("/a/bc matched as a child of /a/b")
	}
	if got.After != "kept" {
		t.Error("top-level member after projects was lost")
	}
	if !strings.HasPrefix(string(data), "{\n  \"numStartups\": 3,\n  \"projects\": {\n    \"/a/b\": {") {
		t.Errorf("layout or order changed:\n%s", data)
	}
	if !bytes.HasSuffix(data, []byte("}\n")) {
		t.Error("trailing newline lost")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Error("lock directory left behind")
	}
}

func TestMoveHistory(t *testing.T) {
	home := setHome(t)
	path := filepath.Join(home, ".claude", "history.jsonl")
	lines := []string{
		`{"display":"one","pastedContents":{},"timestamp":1,"project":"/a/b","sessionId":"s1"}`,
		`{"display":"two","pastedContents":{},"timestamp":2,"project":"/a/bc","sessionId":"s2"}`,
		`{"display":"mentions \"project\":\"/a/b\" in text","pastedContents":{},"timestamp":3,"project":"/c","sessionId":"s3"}`,
		`{"display":"four","pastedContents":{},"timestamp":4,"project":"/a/b/sub","sessionId":"s4"}`,
		`not json "project":"/a/b"`,
		`{"display":"torn tail","project":"/a/b"`,
	}
	// The last line has no newline: a torn write stays as it is.
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	r := Rebase{Old: []string{"/a/b"}, New: "/x/b"}

	if n, err := PlanHistory(r); err != nil || n != 2 {
		t.Fatalf("PlanHistory = %d, %v", n, err)
	}
	if n, err := MoveHistory(r); err != nil || n != 2 {
		t.Fatalf("MoveHistory = %d, %v", n, err)
	}
	if n, err := MoveHistory(r); err != nil || n != 0 {
		t.Fatalf("second MoveHistory = %d, %v (want 0)", n, err)
	}

	got, _ := os.ReadFile(path)
	want := strings.Replace(content, `"project":"/a/b","sessionId":"s1"`, `"project":"/x/b","sessionId":"s1"`, 1)
	want = strings.Replace(want, `"project":"/a/b/sub"`, `"project":"/x/b/sub"`, 1)
	if string(got) != want {
		t.Fatalf("history:\n%s\nwant:\n%s", got, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Error("lock directory left behind")
	}
}

func TestLockWaitsAndBreaksStaleLock(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.json")
	old := lockWait
	lockWait = 100 * time.Millisecond
	defer func() { lockWait = old }()

	if err := os.Mkdir(file+".lock", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := lockFile(file); err == nil {
		t.Fatal("acquired a lock that another process holds")
	}

	past := time.Now().Add(-2 * staleAfter)
	if err := os.Chtimes(file+".lock", past, past); err != nil {
		t.Fatal(err)
	}
	release, err := lockFile(file)
	if err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
	release()
	if _, err := os.Stat(file + ".lock"); !os.IsNotExist(err) {
		t.Error("release left the lock directory")
	}
}

// TestRealFilesRoundTrip checks, read-only, that the encoder reproduces
// the real ~/.claude.json and every line of history.jsonl byte for
// byte. Run with CCCTL_REAL_FILES=1 after a Claude Code update.
func TestRealFilesRoundTrip(t *testing.T) {
	if os.Getenv("CCCTL_REAL_FILES") == "" {
		t.Skip("set CCCTL_REAL_FILES=1 to check the real files")
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	top, err := parseObject(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := encodeObjectIndented(top)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasSuffix(data, []byte("\n")) {
		out = append(out, '\n')
	}
	if !bytes.Equal(out, data) {
		t.Errorf("~/.claude.json does not round-trip (%d vs %d bytes)", len(out), len(data))
	}

	f, err := os.Open(HistoryPath())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rd := bufio.NewReaderSize(f, 1<<20)
	n, bad := 0, 0
	for {
		raw, rerr := rd.ReadBytes('\n')
		line := bytes.TrimRight(raw, "\r\n")
		if len(line) > 0 {
			n++
			m, err := parseObject(line)
			if err == nil {
				var enc []byte
				enc, err = encodeObject(m)
				if err == nil && !bytes.Equal(enc, line) {
					bad++
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	t.Logf("history.jsonl: %d lines, %d do not round-trip", n, bad)
	if bad > 0 {
		t.Fail()
	}

	// Rebase the busiest project to a fake path and back, in memory.
	hist, err := os.ReadFile(HistoryPath())
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, l := range bytes.Split(hist, []byte("\n")) {
		var e struct {
			Project string `json:"project"`
		}
		if json.Unmarshal(l, &e) == nil {
			counts[e.Project]++
		}
	}
	busiest := ""
	for p, c := range counts {
		if c > counts[busiest] {
			busiest = p
		}
	}
	start := time.Now()
	there, moved, err := rewriteHistory(hist, Rebase{Old: []string{busiest}, New: "/ccctl-roundtrip"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	back, movedBack, err := rewriteHistory(there, Rebase{Old: []string{"/ccctl-roundtrip"}, New: busiest})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("busiest project: %d entries rewritten in %v, %d rewritten back", moved, elapsed, movedBack)
	if moved == 0 || moved != movedBack || !bytes.Equal(back, hist) {
		t.Errorf("rewrite there and back changed the bytes")
	}
}
