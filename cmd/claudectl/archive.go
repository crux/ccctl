package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crux/claudectl/internal/archive"
	"github.com/crux/claudectl/internal/proc"
	"github.com/crux/claudectl/internal/project"
	"github.com/spf13/cobra"
)

var archiveCmd = &cobra.Command{
	Use:   "archive <session-id>",
	Short: "Pack a session's files into a .zip, removing the originals",
	Long: `Pack <uuid>.jsonl (and companion <uuid>/ folder if present) into
<uuid>.zip in the same project directory, then remove the originals.
Claude and 'claudectl sessions' no longer see the session; use
'claudectl restore <uuid>' to bring it back.

Refuses to archive an active session or overwrite an existing <uuid>.zip.`,
	Args: cobra.ExactArgs(1),
	RunE: runArchive,
}

func runArchive(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	sourceFile, err := project.FindSessionFile(sessionID)
	if err != nil {
		return err
	}
	if sourceFile == "" {
		return fmt.Errorf("session %s not found in ~/.claude/projects/", sessionID)
	}

	sessionDir := filepath.Dir(sourceFile)
	archivePath := filepath.Join(sessionDir, sessionID+".zip")

	if _, err := os.Stat(archivePath); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite", archivePath)
	}
	if proc.IsFileOpen(sourceFile) {
		return fmt.Errorf("session %s is open by a running claude process; close it first", sessionID)
	}

	items := []string{sessionID + ".jsonl"}
	companion := filepath.Join(sessionDir, sessionID)
	if info, err := os.Stat(companion); err == nil && info.IsDir() {
		items = append(items, sessionID)
	}

	// Create, then validate, THEN delete originals — never destroy data
	// before confirming the archive is readable.
	if err := archive.Create(archivePath, sessionDir, items); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("zip creation failed: %w", err)
	}
	if err := archive.Validate(archivePath); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("zip validation failed: %w", err)
	}
	for _, item := range items {
		if err := os.RemoveAll(filepath.Join(sessionDir, item)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove original %s: %v\n", item, err)
		}
	}

	fmt.Printf("Archived %s\n", sessionID)
	fmt.Printf("  archive: %s\n", archivePath)
	return nil
}
