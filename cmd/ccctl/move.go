package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crux/ccctl/internal/project"
	"github.com/spf13/cobra"
)

var moveCmd = &cobra.Command{
	Use:   "move <session-id> <target-dir>",
	Short: "Move a session into another project's namespace",
	Long: `Move a session's .jsonl (and companion <uuid>/ folder if present)
from its current project slot to the slot for <target-dir>, so
'claude --continue' picks it up there. The session must not be active
when you run this.

After the move, consider running /post-session-move-memory in the
resumed session to reconcile any memory writes made in the old project's
namespace.`,
	Args: cobra.ExactArgs(2),
	RunE: runMove,
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

	sourceFile, err := project.FindSessionFile(sessionID)
	if err != nil {
		return err
	}
	if sourceFile == "" {
		return fmt.Errorf("session %s not found in ~/.claude/projects/", sessionID)
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
