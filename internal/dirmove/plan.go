// Package dirmove moves a project directory together with its Claude
// Code state:
//
//   - the project folders under ~/.claude/projects/ of the directory and
//     of its subdirectories, with sessions, archives and memory
//   - the project entries in ~/.claude.json (copied)
//   - the prompt history in ~/.claude/history.jsonl (moved)
//
// Prepare checks everything and changes nothing. Execute applies a plan
// and undoes the directory and folder renames when a folder rename
// fails.
package dirmove

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/crux/ccctl/internal/claudestate"
	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
)

// Request is one directory move as the user typed it.
type Request struct {
	Src, Dst     string
	SessionsOnly bool // the directory is at Dst already; move the Claude state only
	CrossVolume  bool // allow a move across file systems through /bin/mv
}

// Folder is one project folder under ~/.claude/projects/ that moves.
type Folder struct {
	Name     string // current folder name
	NewName  string
	Rel      string // subdirectory it serves, relative to Src; "" for Src itself
	Sessions int
	Archived int
	Leftover int // <uuid>/ directories without a .jsonl
	Memory   bool
	Merge    bool   // NewName exists; the entries move into it
	Target   string // summary of the existing target folder when Merge
}

// Skipped is a folder whose name matches but whose owner is unclear.
type Skipped struct {
	Name   string
	Reason string
}

// Plan is the checked result of Prepare.
type Plan struct {
	Src          string   // absolute path without symlinks
	Dst          string   // absolute path without symlinks
	SrcAliases   []string // other spellings of Src, as typed
	SessionsOnly bool
	CrossVolume  bool   // Src and the parent of Dst are on different file systems
	Root         string // ~/.claude/projects
	Folders      []Folder
	Skipped      []Skipped
	Config       []claudestate.KeyCopy
	History      int      // prompt history entries to copy
	Warnings     []string // problems that do not stop the move
	Blockers     []string // problems that stop the move; Execute refuses
}

// Rebase maps paths under Src (and its aliases) to Dst.
func (p *Plan) Rebase() claudestate.Rebase {
	return claudestate.Rebase{Old: append([]string{p.Src}, p.SrcAliases...), New: p.Dst}
}

// listProcesses is a variable so tests can replace the process scan.
var listProcesses = proc.ListSessions

// Prepare resolves the paths, finds the affected project folders and
// runs every check. It changes nothing on disk. Invalid paths return
// an error. A running claude or a folder clash goes to Plan.Blockers,
// so a dry run can show the whole plan together with the blockers.
func Prepare(req Request) (*Plan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	p := &Plan{
		SessionsOnly: req.SessionsOnly,
		Root:         filepath.Join(home, ".claude", "projects"),
	}

	absSrc, err := absPath(req.Src)
	if err != nil {
		return nil, err
	}
	absDst, err := absPath(req.Dst)
	if err != nil {
		return nil, err
	}
	if req.SessionsOnly {
		err = p.resolveSessionsOnly(absSrc, absDst)
	} else {
		err = p.resolveMove(absSrc, absDst, req.CrossVolume)
	}
	if err != nil {
		return nil, err
	}
	if absSrc != p.Src {
		p.SrcAliases = []string{absSrc}
	}
	if err := p.checkPaths(home); err != nil {
		return nil, err
	}
	if err := p.findFolders(); err != nil {
		return nil, err
	}
	if err := p.checkActive(); err != nil {
		return nil, err
	}

	r := p.Rebase()
	if p.Config, err = claudestate.PlanConfig(r); err != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("~/.claude.json: %v", err))
	}
	if p.History, err = claudestate.PlanHistory(r); err != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("history.jsonl: %v", err))
	}
	return p, nil
}

func absPath(s string) (string, error) {
	s, err := project.ExpandHome(s)
	if err != nil {
		return "", err
	}
	return filepath.Abs(s)
}

// resolveMove checks a normal move: Src is an existing directory, Dst
// does not exist and its parent does.
func (p *Plan) resolveMove(absSrc, absDst string, allowCross bool) error {
	fi, err := os.Lstat(absSrc)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; Claude Code keys sessions by the link target, so move the target instead", absSrc)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", absSrc)
	}
	if p.Src, err = filepath.EvalSymlinks(absSrc); err != nil {
		return err
	}

	if _, err := os.Lstat(absDst); err == nil {
		return fmt.Errorf("%s exists; ccctl move renames a directory and does not move it into another one (did you mean %s?)",
			absDst, filepath.Join(absDst, filepath.Base(absSrc)))
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absDst))
	if err != nil {
		return fmt.Errorf("parent of %s: %w", absDst, err)
	}
	p.Dst = filepath.Join(parent, filepath.Base(absDst))

	srcDev, err1 := device(p.Src)
	dstDev, err2 := device(parent)
	if err1 == nil && err2 == nil && srcDev != dstDev {
		p.CrossVolume = true
		if !allowCross {
			return fmt.Errorf("%s and %s are on different volumes; add --cross-volume to copy and delete with /bin/mv", p.Src, parent)
		}
	}
	return nil
}

// resolveSessionsOnly checks a move that the user did by hand: Src is
// gone and Dst is an existing directory.
func (p *Plan) resolveSessionsOnly(absSrc, absDst string) error {
	if _, err := os.Lstat(absSrc); err == nil {
		return fmt.Errorf("%s still exists; drop --sessions-only to move it", absSrc)
	}
	p.Src = absSrc
	if parent, err := filepath.EvalSymlinks(filepath.Dir(absSrc)); err == nil {
		p.Src = filepath.Join(parent, filepath.Base(absSrc))
	}
	fi, err := os.Stat(absDst)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", absDst)
	}
	p.Dst, err = filepath.EvalSymlinks(absDst)
	return err
}

func (p *Plan) checkPaths(home string) error {
	claudeDir := filepath.Join(home, ".claude")
	switch {
	case p.Src == p.Dst:
		return fmt.Errorf("source and target are the same path")
	case within(p.Dst, p.Src):
		return fmt.Errorf("cannot move %s into itself", p.Src)
	case p.Src == "/" || p.Src == home:
		return fmt.Errorf("refusing to move %s", p.Src)
	case within(claudeDir, p.Src) || within(p.Src, claudeDir):
		return fmt.Errorf("refusing to move %s: it holds or is inside ~/.claude", p.Src)
	}
	return nil
}

// within reports whether path is dir or lies below dir.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+"/")
}

func device(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil
}

// findFolders matches project folders to Src and its subdirectories.
//
// Claude Code names a folder by replacing '/', '.', ' ' and '@' in the
// directory path with '-'. The name is therefore ambiguous: for Src
// /a/b, the folder -a-b-c can serve the child /a/b/c or the sibling
// /a/b-c. A folder is matched by its sessions first: a session whose
// last cwd encodes to the folder name tells which directory owns it.
// Without such a session, the folder of Src itself is taken by name,
// and a child folder only when the subdirectory exists.
func (p *Plan) findFolders() error {
	entries, err := os.ReadDir(p.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	encOld := project.EncodePath(p.Src)
	encNew := project.EncodePath(p.Dst)
	tree := p.Src
	if p.SessionsOnly {
		tree = p.Dst
	}
	olds := append([]string{p.Src}, p.SrcAliases...)

	var problems []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || (name != encOld && !strings.HasPrefix(name, encOld+"-")) {
			continue
		}
		rest := name[len(encOld):]
		dir := filepath.Join(p.Root, name)
		f := Folder{Name: name, NewName: encNew + rest}
		owners := countFolder(dir, &f)

		var inside, outside []string
		for _, cwd := range owners {
			if withinAny(cwd, olds) {
				inside = append(inside, cwd)
			} else {
				outside = append(outside, cwd)
			}
		}
		switch {
		case len(inside) > 0 && len(outside) > 0:
			problems = append(problems, fmt.Sprintf(
				"%s holds sessions of %s and of %s; Claude Code maps both paths to this one folder",
				name, inside[0], outside[0]))
			continue
		case len(inside) > 0:
			f.Rel = relTo(inside[0], olds)
		case len(outside) > 0:
			if rest == "" {
				p.Skipped = append(p.Skipped, Skipped{name, "its sessions belong to " + outside[0]})
			}
			continue
		case rest == "":
			// Name match for Src itself.
		default:
			rel, ok := findChild(tree, rest)
			if !ok {
				p.Skipped = append(p.Skipped, Skipped{name, "no session names its directory, and no matching subdirectory exists"})
				continue
			}
			f.Rel = rel
		}

		target := filepath.Join(p.Root, f.NewName)
		if _, err := os.Lstat(target); err == nil {
			f.Merge = true
			problems = append(problems, p.checkMerge(&f, dir, target)...)
		}
		p.Folders = append(p.Folders, f)
	}
	sort.Slice(p.Folders, func(i, j int) bool { return p.Folders[i].Name < p.Folders[j].Name })

	// A target that is itself one of the moved folders would carry the
	// merged sessions away again. Example: /w/proj to /w/proj-x, where
	// /w/proj/x has the folder name the new /w/proj-x needs.
	moving := make(map[string]bool, len(p.Folders))
	for _, f := range p.Folders {
		moving[f.Name] = true
	}
	for _, f := range p.Folders {
		if moving[f.NewName] {
			problems = append(problems, fmt.Sprintf(
				"target folder %s is one of the folders that move; move in two steps through another name", f.NewName))
		}
	}
	p.Blockers = append(p.Blockers, problems...)
	return nil
}

// checkMerge fills the merge details of f and returns the clashes that
// block the merge. It also warns when the target folder serves another
// directory whose path encodes to the same name.
func (p *Plan) checkMerge(f *Folder, src, target string) []string {
	var tf Folder
	tf.Name = f.NewName
	owners := countFolder(target, &tf)
	f.Target = describe(tf)

	newPath := p.Dst
	if f.Rel != "" {
		newPath = filepath.Join(p.Dst, f.Rel)
	}
	for _, o := range owners {
		if o != newPath {
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"%s also holds sessions of %s; Claude Code maps %s to the same folder", f.NewName, o, newPath))
			break
		}
	}

	clashes := mergeClashes(src, target)
	if len(clashes) == 0 {
		return nil
	}
	shown := clashes
	if len(shown) > 3 {
		shown = append(shown[:3:3], fmt.Sprintf("%d more", len(clashes)-3))
	}
	return []string{fmt.Sprintf("cannot merge %s into %s: %s exist in both",
		f.Name, f.NewName, strings.Join(shown, ", "))}
}

// mergeClashes lists the entries of src that block a merge into dst.
// Every entry moves; only a name present in both blocks. An empty
// directory counts as absent, and .DS_Store never blocks.
func mergeClashes(src, dst string) []string {
	entries, err := os.ReadDir(src)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if n == ".DS_Store" {
			continue
		}
		dp := filepath.Join(dst, n)
		dfi, err := os.Lstat(dp)
		if err != nil {
			continue
		}
		if e.IsDir() && (isEmptyDir(filepath.Join(src, n)) || dfi.IsDir() && isEmptyDir(dp)) {
			continue
		}
		if e.IsDir() {
			n += "/"
		}
		out = append(out, n)
	}
	return out
}

func isEmptyDir(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	return len(names) == 0 && err != nil
}

func withinAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if within(path, d) {
			return true
		}
	}
	return false
}

func relTo(path string, dirs []string) string {
	for _, d := range dirs {
		if path == d {
			return ""
		}
		if strings.HasPrefix(path, d+"/") {
			return path[len(d)+1:]
		}
	}
	return path
}

// countFolder fills the counts of f and returns the cwds of sessions
// whose last cwd encodes to the folder name. Sessions moved in with
// `ccctl move` and not resumed since carry a foreign cwd and count as
// unknown.
func countFolder(dir string, f *Folder) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	seen := map[string]bool{}
	var owners []string
	for _, e := range entries {
		n := e.Name()
		switch {
		case e.IsDir() && n == "memory":
			f.Memory = !isEmptyDir(filepath.Join(dir, n))
		case e.IsDir():
			if !names[n+".jsonl"] {
				f.Leftover++
			}
		case strings.HasSuffix(n, ".zip"):
			f.Archived++
		case strings.HasSuffix(n, ".jsonl"):
			f.Sessions++
			cwd, err := session.ReadCwd(filepath.Join(dir, n))
			if err == nil && cwd != "" && project.EncodePath(cwd) == f.Name && !seen[cwd] {
				seen[cwd] = true
				owners = append(owners, cwd)
			}
		}
	}
	sort.Strings(owners)
	return owners
}

// findChild looks below root for a directory whose path relative to
// root encodes to rest. rest starts with the '-' that stands for '/'.
// Only branches whose encoded name matches are entered.
func findChild(root, rest string) (string, bool) {
	if !strings.HasPrefix(rest, "-") {
		return "", false
	}
	rest = rest[1:]
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		enc := project.EncodePath(e.Name())
		if rest == enc {
			return e.Name(), true
		}
		if strings.HasPrefix(rest, enc+"-") {
			if sub, ok := findChild(filepath.Join(root, e.Name()), rest[len(enc):]); ok {
				return e.Name() + "/" + sub, true
			}
		}
	}
	return "", false
}

func describe(f Folder) string {
	parts := []string{plural(f.Sessions, "session")}
	if f.Archived > 0 {
		parts = append(parts, fmt.Sprintf("%d archived", f.Archived))
	}
	if f.Leftover > 0 {
		parts = append(parts, plural(f.Leftover, "leftover session dir"))
	}
	if f.Memory {
		parts = append(parts, "memory")
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Describe returns a short summary such as "12 sessions, 3 archived, memory".
func (f Folder) Describe() string { return describe(f) }

// checkActive refuses the move when a claude process works inside the
// directory or has a session open in one of the folders.
func (p *Plan) checkActive() error {
	procs, err := listProcesses()
	if err != nil {
		return fmt.Errorf("list claude processes: %w", err)
	}
	dirs := make([]string, 0, len(p.Folders))
	for _, f := range p.Folders {
		dirs = append(dirs, filepath.Join(p.Root, f.Name))
	}
	trees := []string{p.Src}
	if p.SessionsOnly {
		trees = append(trees, p.Dst)
	}
	for _, pr := range procs {
		inTree := pr.Cwd != "" && withinAny(pr.Cwd, trees)
		inFolder := pr.SessionFile != "" && withinAny(filepath.Dir(pr.SessionFile), dirs)
		if inTree || inFolder {
			p.Blockers = append(p.Blockers, fmt.Sprintf("claude pid %d is running in %s; quit it first", pr.PID, pr.Cwd))
		}
	}
	return nil
}

// BlockedError returns an error listing the blockers, or nil.
func (p *Plan) BlockedError() error {
	if len(p.Blockers) == 0 {
		return nil
	}
	return fmt.Errorf("nothing changed:\n  %s", strings.Join(p.Blockers, "\n  "))
}
