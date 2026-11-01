package main

import (
	"fmt"

	"github.com/crux/ccctl/internal/archive"
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
	uuid := args[0]
	res, err := archive.RestoreSession(uuid)
	if err != nil {
		return err
	}
	fmt.Printf("Restored %s\n", uuid)
	fmt.Printf("  from: %s\n", res.ArchivePath)
	fmt.Printf("    to: %s\n", res.JSONLPath)
	return nil
}
