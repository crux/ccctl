package tui

import (
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
)

// projectsLoadedMsg carries the enumerated project list for the browse
// frame. The slice is already sorted newest-first.
type projectsLoadedMsg struct {
	projects []project.Info
	err      error
}

// sessionsLoadedMsg carries the sessions for one project directory.
// projectDir is echoed back so a stale load message (from a project we
// no longer have the cursor on) can be dropped.
type sessionsLoadedMsg struct {
	projectDir string
	rows       []session.Row
	err        error
}

// activeLoadedMsg carries the current set of running claude processes.
type activeLoadedMsg struct {
	set proc.ActiveSet
}

// loadProjectsCmd enumerates ~/.claude/projects/, filters out system-tmp
// projects when includeTmp is false, and returns a sorted (newest-first)
// list via a projectsLoadedMsg.
func loadProjectsCmd(includeTmp bool) tea.Cmd {
	return func() tea.Msg {
		all, err := project.ListProjects()
		if err != nil {
			return projectsLoadedMsg{err: err}
		}
		var kept []project.Info
		for _, p := range all {
			if !includeTmp && project.IsTmp(p) {
				continue
			}
			kept = append(kept, p)
		}
		sort.Slice(kept, func(i, j int) bool { return kept[i].Latest.After(kept[j].Latest) })
		return projectsLoadedMsg{projects: kept}
	}
}

// loadSessionsCmd lists sessions for projectDir. Live sessions only for
// now; the archived toggle lives with the tmp toggle in a later step.
func loadSessionsCmd(projectDir string) tea.Cmd {
	return func() tea.Msg {
		rows, err := session.ListInProject(projectDir, false)
		return sessionsLoadedMsg{projectDir: projectDir, rows: rows, err: err}
	}
}

// loadActiveCmd resolves running claude processes to their UUIDs.
func loadActiveCmd() tea.Cmd {
	return func() tea.Msg {
		set, _ := proc.ActiveUUIDs()
		return activeLoadedMsg{set: set}
	}
}
