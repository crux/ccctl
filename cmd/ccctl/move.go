package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crux/ccctl/internal/dirmove"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
	"github.com/spf13/cobra"
)

var (
	moveFromDir      string
	moveDryRun       bool
	moveCrossVolume  bool
	moveSessionsOnly bool
)

var moveCmd = &cobra.Command{
	Use:   "move <session-id | dir> <target>",
	Short: "Move a session to another project, or a directory with its sessions",
	Long: `ccctl move works like mv: the first argument decides what moves.

ccctl move <session-id> <dir>

  Moves one session's .jsonl (and companion <uuid>/ folder if present)
  from its current project slot to the slot for <dir>, so
  'claude --continue' picks it up there. The session must not be active
  when you run this.

  By default, <session-id> is looked up anywhere under
  ~/.claude/projects/, and tab completion offers only sessions in the
  project namespace for the current directory. Pass --from <dir> to
  scope BOTH completion and the lookup to that directory's namespace;
  the move then fails cleanly if <session-id> is not there. Use the
  --from=<dir> form (with =) for tab-completion to list session IDs
  reliably — cobra's zsh completion filters out candidates behind the
  space-separated form.

  Memory files that the session wrote stay in the old project's
  memory/ folder. After the move, copy the ones that belong to the new
  project.

ccctl move <dir> <new-path>

  Renames <dir> to <new-path> and takes its Claude Code state along:

    - the project folders under ~/.claude/projects/ of <dir> and of its
      subdirectories, with sessions, archives and memory
    - the project entries in ~/.claude.json (trust, allowed tools, MCP
      servers), copied under Claude Code's lock
    - the prompt history in ~/.claude/history.jsonl, moved under
      Claude Code's lock

  <new-path> must not exist: the command renames, it does not move
  <dir> into another directory. It refuses while a claude process works
  inside <dir>. Claude processes elsewhere can keep running.

  When the project folder for <new-path> exists already (left over from
  an earlier directory at that path, or a path that Claude Code encodes
  to the same name), the entries merge into it. A name present in both
  folders stops the move; an empty directory counts as absent.

  --dry-run         show the plan and change nothing
  --cross-volume    allow a move to another volume (copy and delete
                    through /bin/mv)
  --sessions-only   <dir> was moved by hand already; move the Claude
                    Code state only (also finishes a failed move)

An argument that is a session id and also an existing path is
ambiguous. Write ./<id> for the path.`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: moveCompleter,
	RunE:              runMove,
}

func init() {
	moveCmd.Flags().StringVar(&moveFromDir, "from", "",
		"source project directory used to scope session-id completion (default: current directory)")
	moveCmd.RegisterFlagCompletionFunc("from", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	})
	moveCmd.Flags().BoolVarP(&moveDryRun, "dry-run", "n", false, "show what would move and change nothing")
	moveCmd.Flags().BoolVar(&moveCrossVolume, "cross-volume", false, "directory move: allow a target on another volume")
	moveCmd.Flags().BoolVar(&moveSessionsOnly, "sessions-only", false, "directory move: the directory is moved already, move the Claude state only")
}

// resolveScopeDir normalises a user-supplied directory to its realpath,
// handling tilde expansion (shells don't expand ~ inside a flag value)
// and relative paths. Empty input returns the realpath of $PWD.
func resolveScopeDir(dir string) (string, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	dir, err := project.ExpandHome(dir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

var sessionIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type sourceKind int

const (
	sourceSession sourceKind = iota + 1
	sourceDir
)

// classifySource decides what the first argument of move names, like
// mv decides between file and directory. With sessionsOnly the
// directory is gone already, so the argument is always a path.
func classifySource(arg string, sessionsOnly bool) (sourceKind, error) {
	if sessionsOnly {
		return sourceDir, nil
	}
	isID := sessionIDRE.MatchString(arg)
	path, err := project.ExpandHome(arg)
	if err != nil {
		return 0, err
	}
	_, statErr := os.Lstat(path)
	exists := statErr == nil
	switch {
	case isID && exists:
		return 0, fmt.Errorf("%s is a session id and also a path here; write ./%s for the path", arg, arg)
	case isID:
		return sourceSession, nil
	case exists:
		return sourceDir, nil
	default:
		return 0, fmt.Errorf("%s: no such directory, and not a session id", arg)
	}
}

// moveCompleter completes the first positional with session ids from
// the scope project namespace (default: realpath($PWD), override:
// --from <dir>). When the word looks like a path or no session id
// matches, it falls through to directory completion. The target
// positional always completes directories.
func moveCompleter(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 1:
		return nil, cobra.ShellCompDirectiveFilterDirs
	case 0:
		// handled below
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if strings.ContainsAny(toComplete, "/.~") {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}

	real, err := resolveScopeDir(moveFromDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
	projDir := project.Dir(real)
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}

	type cand struct {
		uuid, title string
		mtime       int64
	}
	var cands []cand
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		uuid := strings.TrimSuffix(name, ".jsonl")
		if !strings.HasPrefix(uuid, toComplete) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(uuid string, path string, mtime int64) {
			defer wg.Done()
			m, _ := session.ReadMeta(path)
			mu.Lock()
			cands = append(cands, cand{uuid: uuid, title: m.Display(), mtime: mtime})
			mu.Unlock()
		}(uuid, filepath.Join(projDir, name), info.ModTime().UnixNano())
	}
	wg.Wait()
	if len(cands) == 0 {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime > cands[j].mtime })

	out := make([]string, 0, len(cands))
	for _, c := range cands {
		mt := time.Unix(0, c.mtime).Format("2006-01-02 15:04")
		out = append(out, c.uuid+"\t"+c.title+" · "+mt)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func runMove(cmd *cobra.Command, args []string) error {
	kind, err := classifySource(args[0], moveSessionsOnly)
	if err != nil {
		return err
	}
	if kind == sourceDir {
		if moveFromDir != "" {
			return fmt.Errorf("--from applies to session moves only")
		}
		return runDirMove(args[0], args[1])
	}
	if moveCrossVolume || moveSessionsOnly {
		return fmt.Errorf("--cross-volume and --sessions-only apply to directory moves only")
	}
	return runSessionMove(args[0], args[1])
}

func runDirMove(src, dst string) error {
	plan, err := dirmove.Prepare(dirmove.Request{
		Src:          src,
		Dst:          dst,
		SessionsOnly: moveSessionsOnly,
		CrossVolume:  moveCrossVolume,
	})
	if err != nil {
		return err
	}
	for _, w := range plan.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if moveDryRun {
		printDirMove(plan, nil)
		if len(plan.Blockers) > 0 {
			fmt.Println()
			fmt.Println("A real run would refuse:")
			for _, b := range plan.Blockers {
				fmt.Printf("  %s\n", b)
			}
			return fmt.Errorf("blocked")
		}
		return nil
	}
	if err := plan.BlockedError(); err != nil {
		return err
	}
	res, err := dirmove.Execute(plan)
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	printDirMove(plan, res)
	return nil
}

// printDirMove prints the plan (res == nil: dry run) or the outcome.
func printDirMove(p *dirmove.Plan, res *dirmove.Result) {
	dry := res == nil
	verb := func(done, would string) string {
		if dry {
			return would
		}
		return done
	}

	if dry {
		fmt.Println("Dry run: nothing changed.")
		fmt.Println()
	}
	switch {
	case p.SessionsOnly:
		fmt.Printf("Directory moved already: %s\n", p.Dst)
	default:
		how := ""
		if p.CrossVolume {
			how = " (across volumes)"
		}
		fmt.Printf("%s directory%s\n", verb("Moved", "Would move"), how)
		fmt.Printf("  from: %s\n", p.Src)
		fmt.Printf("    to: %s\n", p.Dst)
	}

	fmt.Println()
	if len(p.Folders) == 0 {
		fmt.Println("Project folders: none found")
	} else {
		fmt.Printf("Project folders in %s/: %s %d\n", p.Root, verb("renamed", "to rename"), len(p.Folders))
		width := 1
		for _, f := range p.Folders {
			width = max(width, len(f.Rel))
		}
		for _, f := range p.Folders {
			rel := f.Rel
			if rel == "" {
				rel = "."
			}
			fmt.Printf("  %-*s  %s\n", width, rel, f.Describe())
			if f.Merge {
				fmt.Printf("  %-*s  %s into existing folder: %s\n", width, "", verb("merged", "merges"), f.Target)
			}
		}
		fmt.Printf("  prefix  %s\n      →   %s\n", project.EncodePath(p.Src), project.EncodePath(p.Dst))
	}
	if len(p.Skipped) > 0 {
		fmt.Println()
		fmt.Printf("Left in place: %d\n", len(p.Skipped))
		for _, s := range p.Skipped {
			fmt.Printf("  %s\n    %s\n", s.Name, s.Reason)
		}
	}

	config := p.Config
	history := p.History
	if res != nil {
		config, history = res.Config, res.History
	}
	copied, kept := 0, 0
	for _, c := range config {
		if c.Kept {
			kept++
		} else {
			copied++
		}
	}
	fmt.Println()
	fmt.Printf("Settings in ~/.claude.json: %d %s", copied, verb("copied", "to copy"))
	if kept > 0 {
		fmt.Printf(", %d kept (target has its own)", kept)
	}
	fmt.Println()
	fmt.Printf("Prompt history: %d entries %s\n", history, verb("moved", "to move"))

	if !dry {
		fmt.Println()
		fmt.Printf("cd %q && claude --continue\n", p.Dst)
	}
}

func runSessionMove(sessionID, targetDir string) error {
	targetDir, err := project.ExpandHome(targetDir)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}
	realTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		return fmt.Errorf("resolve target dir: %w", err)
	}
	targetPath := project.Dir(realTarget)

	var sourceFile string
	if moveFromDir != "" {
		realFrom, err := resolveScopeDir(moveFromDir)
		if err != nil {
			return fmt.Errorf("resolve --from dir: %w", err)
		}
		candidate := filepath.Join(project.Dir(realFrom), sessionID+".jsonl")
		if _, err := os.Stat(candidate); err != nil {
			return fmt.Errorf("session %s not found in --from %s", sessionID, realFrom)
		}
		sourceFile = candidate
	} else {
		sourceFile, err = project.FindSessionFile(sessionID)
		if err != nil {
			return err
		}
		if sourceFile == "" {
			return fmt.Errorf("session %s not found in ~/.claude/projects/", sessionID)
		}
	}

	sourceDir := filepath.Dir(sourceFile)
	if sourceDir == targetPath {
		fmt.Println("Session is already in the target project slot.")
		return nil
	}

	if moveDryRun {
		fmt.Println("Dry run: nothing changed.")
		fmt.Println()
		fmt.Printf("Would move %s\n", sessionID)
		fmt.Printf("  from: %s\n", sourceDir)
		fmt.Printf("    to: %s\n", targetPath)
		return nil
	}

	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return err
	}

	targetFile := filepath.Join(targetPath, sessionID+".jsonl")
	if err := os.Rename(sourceFile, targetFile); err != nil {
		return fmt.Errorf("move .jsonl: %w", err)
	}

	// Companion folder is optional; its move is best-effort — the primary
	// move already succeeded.
	companion := filepath.Join(sourceDir, sessionID)
	if info, err := os.Stat(companion); err == nil && info.IsDir() {
		if err := os.Rename(companion, filepath.Join(targetPath, sessionID)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: companion folder not moved: %v\n", err)
		}
	}

	fmt.Printf("Moved %s\n", sessionID)
	fmt.Printf("  from: %s\n", sourceDir)
	fmt.Printf("    to: %s\n", targetPath)
	fmt.Println()
	fmt.Printf("cd %q && claude --continue\n", realTarget)
	fmt.Println()
	fmt.Println("Memory files this session wrote stay in the old project's folder:")
	fmt.Printf("  %s\n", filepath.Join(sourceDir, "memory"))
	fmt.Println("Copy the ones that belong to the new project.")
	return nil
}
