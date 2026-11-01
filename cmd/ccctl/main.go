// Command ccctl is a personal toolkit for inspecting and operating on
// Claude Code sessions and the local project namespaces that hold them.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ccctl",
	Short: "Personal toolkit for Claude Code session management",
	Long: `ccctl inspects and operates on Claude Code sessions stored under
~/.claude/projects/<encoded-project>/.

With no subcommand, prints the same overview as 'ccctl status'. Use
'ccctl --help' or 'ccctl <subcommand> --help' for the full
command set.`,
	Args: cobra.NoArgs,
	RunE: runStatusDefault,
}

// runStatusDefault runs the status command when the user invokes
// `ccctl` with no subcommand, then prints a one-line hint pointing
// at --help so the broader command set stays discoverable.
func runStatusDefault(cmd *cobra.Command, args []string) error {
	if err := runStatus(cmd, args); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Run 'ccctl --help' for the full command list.")
	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ccctl:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(projectsCmd)
	rootCmd.AddCommand(sessionsCmd)
	rootCmd.AddCommand(moveCmd)
	rootCmd.AddCommand(archiveCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(focusCmd)
	rootCmd.AddCommand(takeoverCmd)
	rootCmd.AddCommand(findCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(uiCmd)
	// Silence cobra's own error echo — main.go prints via Fprintln above
	// so we can prefix with "ccctl:".
	rootCmd.SilenceErrors = true
	// Flip SilenceUsage once args validation has passed, so arg-
	// validation errors still auto-print the Usage block (helps the
	// user recover from a wrong invocation) but runtime errors from
	// RunE don't drag the Usage block into the output as noise.
	// PersistentPreRun fires AFTER cobra validates args and BEFORE
	// Run/RunE, which is exactly the hinge we want.
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	}
}
