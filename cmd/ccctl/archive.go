package main

import (
	"fmt"

	"github.com/crux/ccctl/internal/archive"
	"github.com/spf13/cobra"
)

var archiveCmd = &cobra.Command{
	Use:   "archive <session-id>",
	Short: "Pack a session's files into a .zip, removing the originals",
	Long: `Pack <uuid>.jsonl (and companion <uuid>/ folder if present) into
<uuid>.zip in the same project directory, then remove the originals.
Claude and 'ccctl sessions' no longer see the session; use
'ccctl restore <uuid>' to bring it back.

Refuses to archive an active session or overwrite an existing <uuid>.zip.`,
	Args: cobra.ExactArgs(1),
	RunE: runArchive,
}

func runArchive(cmd *cobra.Command, args []string) error {
	uuid := args[0]
	res, err := archive.ArchiveSession(uuid)
	if err != nil {
		return err
	}
	fmt.Printf("Archived %s\n", uuid)
	fmt.Printf("  archive: %s\n", res.ArchivePath)
	return nil
}
