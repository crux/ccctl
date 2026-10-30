package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
	"github.com/spf13/cobra"
)

var moveFromDir string

var moveCmd = &cobra.Command{
	Use:   "move <session-id> <target-dir>",
	Short: "Move a session into another project's namespace",
	Long: `Move a session's .jsonl (and companion <uuid>/ folder if present)
from its current project slot to the slot for <target-dir>, so
'claude --continue' picks it up there. The session must not be active
when you run this.

By default, <session-id> is looked up anywhere under
~/.claude/projects/, and tab completion offers only sessions in the
project namespace for the current directory. Pass --from <dir> to
scope BOTH completion and the lookup to that directory's namespace;
the move then fails cleanly if <session-id> is not there.

After the move, consider running /post-session-move-memory in the
resumed session to reconcile any memory writes made in the old project's
namespace.`,
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
}

// moveCompleter completes the session-id positional from sessions
// stored under the scope project namespace (default: realpath($PWD),
// override: --from <dir>). The target-dir positional falls through to
// directory completion.
func moveCompleter(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 1:
		return nil, cobra.ShellCompDirectiveFilterDirs
	case 0:
		// handled below
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	scopeDir := moveFromDir
	if scopeDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		scopeDir = wd
	}
	real, err := filepath.EvalSymlinks(scopeDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	projDir := project.Dir(real)
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
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
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime > cands[j].mtime })

	out := make([]string, 0, len(cands))
	for _, c := range cands {
		mt := time.Unix(0, c.mtime).Format("2006-01-02 15:04")
		out = append(out, c.uuid+"\t"+c.title+" · "+mt)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func runMove(cmd *cobra.Command, args []string) error {
	sessionID := args[0]
	targetDir := args[1]

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
		absFrom, err := filepath.Abs(moveFromDir)
		if err != nil {
			return err
		}
		realFrom, err := filepath.EvalSymlinks(absFrom)
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
	fmt.Println("If this session wrote memory in the old project's namespace, run")
	fmt.Println("  /post-session-move-memory")
	fmt.Println("after resuming to reconcile.")
	return nil
}
