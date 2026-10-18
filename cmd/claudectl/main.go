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

Each subcommand targets one aspect: ps lists running sessions, sessions
lists stored sessions for a project, move/archive/restore manage session
file lifecycle.`,
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
	// Silence cobra's default error echo — main prints via Fprintln above.
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
}
