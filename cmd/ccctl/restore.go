package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crux/ccctl/internal/archive"
	"github.com/crux/ccctl/internal/project"
	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore <session-id>",
	Short: "Unpack an archived session .zip in place",
	Long: `Unpack <uuid>.zip in place, then remove the archive. The session
becomes visible to Claude and 'ccctl sessions' again.

Refuses to overwrite an existing <uuid>.jsonl.`,
	Args: cobra.ExactArgs(1),
	RunE: runRestore,
}

func runRestore(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	archivePath, err := project.FindArchive(sessionID)
	if err != nil {
		return err
	}
	if archivePath == "" {
		return fmt.Errorf("archive %s.zip not found in ~/.claude/projects/", sessionID)
	}

	sessionDir := filepath.Dir(archivePath)
	targetFile := filepath.Join(sessionDir, sessionID+".jsonl")

	if _, err := os.Stat(targetFile); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite", targetFile)
	}

	if err := archive.Extract(archivePath, sessionDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	if err := os.Remove(archivePath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to remove archive: %v\n", err)
	}

	fmt.Printf("Restored %s\n", sessionID)
	fmt.Printf("  from: %s\n", archivePath)
	fmt.Printf("    to: %s\n", targetFile)
	return nil
}
