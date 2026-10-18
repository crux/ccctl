// Command claudectl is a personal toolkit for inspecting and operating on
// Claude Code sessions and the local project namespaces that hold them.
//
// Supersedes an earlier shell implementation; the companion claude.zsh
// keeps the original claude-* command names as thin shims that call this
// binary.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "claudectl",
	Short: "Personal toolkit for Claude Code session management",
	Long: `claudectl inspects and operates on Claude Code sessions stored under
~/.claude/projects/<encoded-project>/.

With no subcommand, prints the same overview as 'claudectl status'. Use
'claudectl --help' or 'claudectl <subcommand> --help' for the full
command set.`,
	Args: cobra.NoArgs,
	RunE: runStatusDefault,
}

// runStatusDefault runs the status command when the user invokes
// `claudectl` with no subcommand, then prints a one-line hint pointing
// at --help so the broader command set stays discoverable.
func runStatusDefault(cmd *cobra.Command, args []string) error {
	if err := runStatus(cmd, args); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Run 'claudectl --help' for the full command list.")
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "claudectl:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(sessionsCmd)
	rootCmd.AddCommand(moveCmd)
	rootCmd.AddCommand(archiveCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(whereCmd)
	rootCmd.AddCommand(focusCmd)
	rootCmd.AddCommand(takeoverCmd)
	rootCmd.AddCommand(findCmd)
	rootCmd.AddCommand(statusCmd)
	// Silence cobra's default error echo — main prints via Fprintln above.
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
}
