package dirmove

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
)

// fixture is a fake home with one project tree:
//
//	work/proj            folder with 2 sessions, an archive, memory
//	work/proj/sub        folder with 1 session
//	work/proj/sub/.deep  folder with an archive only (matched by subdirectory)
//	work/proj-old        sibling; its folder shares the name prefix
//	(gone)               folder -…-proj-gone without sessions or subdirectory
type fixture struct {
	t     *testing.T
	home  string
	work  string
	src   string
	root  string
	procs []proc.Info
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, home: filepath.Join(base, "home"), work: filepath.Join(base, "work")}
	f.src = filepath.Join(f.work, "proj")
	f.root = filepath.Join(f.home, ".claude", "projects")
	t.Setenv("HOME", f.home)

	orig := listProcesses
	listProcesses = func() ([]proc.Info, error) { return f.procs, nil }
	t.Cleanup(func() { listProcesses = orig })

	f.mkdir(filepath.Join(f.src, "sub", ".deep"))
	f.mkdir(filepath.Join(f.work, "proj-old"))
	f.write(filepath.Join(f.src, "README"), "hello")

	f.session(f.src, "s1", f.src)
	f.session(f.src, "s2", f.src)
	f.write(filepath.Join(f.folder(f.src), "a1.zip"), "zip")
	f.mkdir(filepath.Join(f.folder(f.src), "s1"))
	f.write(filepath.Join(f.folder(f.src), "memory", "MEMORY.md"), "- note\n")
	f.session(filepath.Join(f.src, "sub"), "s3", filepath.Join(f.src, "sub"))
	f.write(filepath.Join(f.folder(filepath.Join(f.src, "sub", ".deep")), "a2.zip"), "zip")
	f.session(filepath.Join(f.work, "proj-old"), "s4", filepath.Join(f.work, "proj-old"))
	f.write(filepath.Join(f.root, project.EncodePath(f.src)+"-gone", "a3.zip"), "zip")

	cfg := map[string]any{
		"numStartups": 1,
		"projects": map[string]any{
			f.src:                             map[string]any{"hasTrustDialogAccepted": true},
			filepath.Join(f.src, "sub"):       map[string]any{"allowedTools": []string{"Read"}},
			filepath.Join(f.work, "proj-old"): map[string]any{"hasTrustDialogAccepted": false},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	f.write(filepath.Join(f.home, ".claude.json"), string(data))

	var hist []string
	for i, p := range []string{f.src, filepath.Join(f.src, "sub"), filepath.Join(f.work, "proj-old")} {
		line, _ := json.Marshal(map[string]any{"display": "prompt", "pastedContents": map[string]any{}, "timestamp": i, "project": p, "sessionId": "x"})
		hist = append(hist, string(line))
	}
	f.write(filepath.Join(f.home, ".claude", "history.jsonl"), strings.Join(hist, "\n")+"\n")
	return f
}

func (f *fixture) folder(dir string) string {
	return filepath.Join(f.root, project.EncodePath(dir))
}

func (f *fixture) mkdir(p string) {
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) write(p, s string) {
	f.mkdir(filepath.Dir(p))
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) session(dir, id, cwd string) {
	f.write(filepath.Join(f.folder(dir), id+".jsonl"), `{"type":"user","cwd":"`+cwd+`"}`+"\n")
}

func (f *fixture) exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestMoveDirectoryWithState(t *testing.T) {
	f := newFixture(t)
	dst := filepath.Join(f.work, "renamed")

	p, err := Prepare(Request{Src: f.src, Dst: dst})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Folders) != 3 {
		t.Fatalf("folders = %+v", p.Folders)
	}
	rels := []string{p.Folders[0].Rel, p.Folders[1].Rel, p.Folders[2].Rel}
	if strings.Join(rels, ",") != ",sub,sub/.deep" {
		t.Errorf("rels = %q", rels)
	}
	if got := p.Folders[0].Describe(); got != "2 sessions, 1 archived, memory" {
		t.Errorf("describe = %q", got)
	}
	if len(p.Skipped) != 1 || !strings.HasSuffix(p.Skipped[0].Name, "-proj-gone") {
		t.Errorf("skipped = %+v", p.Skipped)
	}
	if len(p.Config) != 2 || p.History != 2 {
		t.Errorf("config = %+v, history = %d", p.Config, p.History)
	}

	res, err := Execute(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) > 0 {
		t.Errorf("warnings: %v", res.Warnings)
	}

	if f.exists(f.src) || !f.exists(filepath.Join(dst, "README")) {
		t.Error("directory not moved")
	}
	for _, rel := range []string{"", "sub", "sub/.deep"} {
		oldDir, newDir := f.src, dst
		if rel != "" {
			oldDir, newDir = filepath.Join(f.src, rel), filepath.Join(dst, rel)
		}
		if f.exists(f.folder(oldDir)) || !f.exists(f.folder(newDir)) {
			t.Errorf("folder for %q not renamed", rel)
		}
	}
	if !f.exists(filepath.Join(f.folder(dst), "memory", "MEMORY.md")) || !f.exists(filepath.Join(f.folder(dst), "s1")) {
		t.Error("memory or companion folder did not move with the folder")
	}
	if !f.exists(f.folder(filepath.Join(f.work, "proj-old"))) || !f.exists(filepath.Join(f.root, project.EncodePath(f.src)+"-gone")) {
		t.Error("sibling or unclear folder was touched")
	}

	cfg, _ := os.ReadFile(filepath.Join(f.home, ".claude.json"))
	for _, key := range []string{dst, filepath.Join(dst, "sub"), f.src} {
		if !strings.Contains(string(cfg), `"`+key+`"`) {
			t.Errorf("config lacks key %s", key)
		}
	}
	hist, _ := os.ReadFile(filepath.Join(f.home, ".claude", "history.jsonl"))
	if n := strings.Count(string(hist), "\n"); n != 3 {
		t.Errorf("history has %d lines, want 3", n)
	}
	for _, want := range []string{`"project":"` + dst + `"`, `"project":"` + dst + `/sub"`, `"project":"` + f.work + `/proj-old"`} {
		if !strings.Contains(string(hist), want) {
			t.Errorf("history lacks %s", want)
		}
	}
	if strings.Contains(string(hist), `"project":"`+f.src+`"`) {
		t.Error("history still has entries for the old path")
	}
}

// blocked prepares a move, expects a blocker containing want, and
// checks that Execute refuses and leaves the directory in place.
func (f *fixture) blocked(dst, want string) {
	f.t.Helper()
	p, err := Prepare(Request{Src: f.src, Dst: dst})
	if err != nil {
		f.t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Blockers, "\n"), want) {
		f.t.Fatalf("blockers = %q, want %q", p.Blockers, want)
	}
	if _, err := Execute(p); err == nil {
		f.t.Fatal("Execute ran despite blockers")
	}
	if !f.exists(f.src) || f.exists(dst) {
		f.t.Error("directory moved despite the blockers")
	}
}

func TestRefusesWhileClaudeRunsInside(t *testing.T) {
	f := newFixture(t)
	f.procs = []proc.Info{{PID: 42, Cwd: filepath.Join(f.src, "sub")}}
	f.blocked(filepath.Join(f.work, "x"), "pid 42")
}

func TestIgnoresClaudeRunningElsewhere(t *testing.T) {
	f := newFixture(t)
	f.procs = []proc.Info{{PID: 7, Cwd: filepath.Join(f.work, "proj-old")}}
	if _, err := Prepare(Request{Src: f.src, Dst: filepath.Join(f.work, "x")}); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesExistingTarget(t *testing.T) {
	f := newFixture(t)
	_, err := Prepare(Request{Src: f.src, Dst: filepath.Join(f.work, "proj-old")})
	if err == nil || !strings.Contains(err.Error(), "does not move it into another one") {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeIntoLeftoverFolder(t *testing.T) {
	f := newFixture(t)
	dst := filepath.Join(f.work, "renamed")
	target := f.folder(dst)
	f.session(dst, "leftover", dst)
	f.mkdir(filepath.Join(target, "memory")) // empty: counts as absent
	f.write(filepath.Join(target, ".DS_Store"), "target")
	f.write(filepath.Join(f.folder(f.src), ".DS_Store"), "source")
	f.write(filepath.Join(f.folder(f.src), "sessions-index.json"), "{}")

	p, err := Prepare(Request{Src: f.src, Dst: dst})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || len(p.Warnings) > 0 {
		t.Fatalf("blockers = %q, warnings = %q", p.Blockers, p.Warnings)
	}
	if !p.Folders[0].Merge || p.Folders[0].Target != "1 session" || p.Folders[1].Merge {
		t.Fatalf("folders = %+v", p.Folders)
	}
	if _, err := Execute(p); err != nil {
		t.Fatal(err)
	}

	for _, n := range []string{"leftover.jsonl", "s1.jsonl", "s2.jsonl", "s1", "a1.zip", "memory/MEMORY.md", "sessions-index.json"} {
		if !f.exists(filepath.Join(target, n)) {
			t.Errorf("target lacks %s", n)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(target, ".DS_Store")); string(b) != "target" {
		t.Errorf(".DS_Store = %q, want the target's", b)
	}
	if f.exists(f.folder(f.src)) {
		t.Error("source folder not removed after the merge")
	}
}

func TestMergeBlocksOnMemoryInBoth(t *testing.T) {
	f := newFixture(t)
	dst := filepath.Join(f.work, "renamed")
	f.session(dst, "leftover", dst)
	f.write(filepath.Join(f.folder(dst), "memory", "MEMORY.md"), "- other\n")
	f.blocked(dst, "memory/ exist in both")
}

func TestMergeWarnsOnSharedFolderName(t *testing.T) {
	f := newFixture(t)
	// /work/re-named and /work/re.named both encode to …-work-re-named.
	dst := filepath.Join(f.work, "re-named")
	other := filepath.Join(f.work, "re.named")
	f.session(other, "x1", other)
	p, err := Prepare(Request{Src: f.src, Dst: dst})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || !strings.Contains(strings.Join(p.Warnings, "\n"), "also holds sessions of "+other) {
		t.Fatalf("blockers = %q, warnings = %q", p.Blockers, p.Warnings)
	}
}

func TestMergeRollback(t *testing.T) {
	f := newFixture(t)
	dst := filepath.Join(f.work, "renamed")
	f.session(dst, "leftover", dst)
	p, err := Prepare(Request{Src: f.src, Dst: dst})
	if err != nil {
		t.Fatal(err)
	}
	// A clash appears after the check; entries sorted before it have
	// moved already when the merge stops.
	f.session(dst, "s2", dst)

	if _, err := Execute(p); err == nil || !strings.Contains(err.Error(), "all changes were undone") {
		t.Fatalf("err = %v", err)
	}
	for _, n := range []string{"s1.jsonl", "s2.jsonl", "s1", "a1.zip", "memory/MEMORY.md"} {
		if !f.exists(filepath.Join(f.folder(f.src), n)) {
			t.Errorf("source folder lacks %s after undo", n)
		}
	}
	entries, _ := os.ReadDir(f.folder(dst))
	if len(entries) != 2 {
		t.Errorf("target folder has %d entries after undo, want 2", len(entries))
	}
	if !f.exists(filepath.Join(f.src, "README")) || f.exists(dst) {
		t.Error("directory not moved back")
	}
}

func TestBlocksMergeIntoMovingFolder(t *testing.T) {
	f := newFixture(t)
	// The new name of proj's folder is the current name of proj/sub's.
	f.blocked(filepath.Join(f.work, "proj-sub"), "is one of the folders that move")
}

func TestRefusesMixedFolder(t *testing.T) {
	f := newFixture(t)
	// /work/proj.v2 encodes like /work/proj/v2; give the child folder a
	// session from the sibling as well.
	f.mkdir(filepath.Join(f.src, "v2"))
	f.mkdir(filepath.Join(f.work, "proj.v2"))
	f.session(filepath.Join(f.src, "v2"), "s5", filepath.Join(f.src, "v2"))
	f.session(filepath.Join(f.src, "v2"), "s6", filepath.Join(f.work, "proj.v2"))
	f.blocked(filepath.Join(f.work, "x"), "maps both paths to this one folder")
}

func TestRollbackWhenFolderRenameFails(t *testing.T) {
	f := newFixture(t)
	dst := filepath.Join(f.work, "renamed")
	p, err := Prepare(Request{Src: f.src, Dst: dst})
	if err != nil {
		t.Fatal(err)
	}
	// A folder appears at the second target after the check.
	f.mkdir(filepath.Join(f.root, p.Folders[1].NewName))

	if _, err := Execute(p); err == nil || !strings.Contains(err.Error(), "all changes were undone") {
		t.Fatalf("err = %v", err)
	}
	if !f.exists(filepath.Join(f.src, "README")) || f.exists(dst) {
		t.Error("directory not moved back")
	}
	if !f.exists(filepath.Join(f.folder(f.src), "s1.jsonl")) || f.exists(f.folder(dst)) {
		t.Error("first folder not renamed back")
	}
}

func TestSessionsOnly(t *testing.T) {
	f := newFixture(t)
	dst := filepath.Join(f.work, "by-hand")
	if err := os.Rename(f.src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(Request{Src: dst, Dst: filepath.Join(f.work, "y"), SessionsOnly: true}); err == nil {
		t.Fatal("sessions-only accepted a source that still exists")
	}
	p, err := Prepare(Request{Src: f.src, Dst: dst, SessionsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Folders) != 3 {
		t.Fatalf("folders = %+v", p.Folders)
	}
	if _, err := Execute(p); err != nil {
		t.Fatal(err)
	}
	if !f.exists(f.folder(filepath.Join(dst, "sub", ".deep"))) {
		t.Error("subdirectory folder not renamed")
	}
}

func TestFindChild(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".config", "my app", "x.y"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel, ok := findChild(root, project.EncodePath("/.config/my app/x.y"))
	if !ok || rel != ".config/my app/x.y" {
		t.Fatalf("findChild = %q, %v", rel, ok)
	}
	if _, ok := findChild(root, "-config-nope"); ok {
		t.Fatal("matched a missing directory")
	}
}
