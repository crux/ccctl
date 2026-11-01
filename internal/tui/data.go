package tui

import (
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/crux/ccctl/internal/archive"
	"github.com/crux/ccctl/internal/focus"
	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
)

// activeRefreshInterval is the cadence for the :active view's live
// refresh. 2s matches the k9s-style default — fast enough to feel
// live, slow enough to not hammer lsof.
const activeRefreshInterval = 2 * time.Second

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

// loadSessionsCmd lists sessions for projectDir. includeArchived
// controls whether .zip archives also appear in the list.
func loadSessionsCmd(projectDir string, includeArchived bool) tea.Cmd {
	return func() tea.Msg {
		rows, err := session.ListInProject(projectDir, includeArchived)
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

// activeListLoadedMsg carries the full enumerated active-process list
// for the :active view. The slice is already sorted by PID.
type activeListLoadedMsg struct {
	procs []proc.Info
	err   error
}

// loadActiveListCmd runs the fuller ListClaudeProcesses — one entry
// per running `claude` process with cwd, uuid, title, status. A process
// without a real title (Claude Code's derived placeholder name) gets the
// title from its session file, as `ccctl ps` does.
func loadActiveListCmd() tea.Cmd {
	return func() tea.Msg {
		procs, err := proc.ListClaudeProcesses()
		now := time.Now()
		for i := range procs {
			if procs[i].Title == "" && procs[i].SessionFile != "" {
				procs[i].Title = sessionTitle(procs[i].SessionFile, now)
			}
		}
		return activeListLoadedMsg{procs: procs, err: err}
	}
}

// titleTTL bounds how often the :active view reads a session file for
// its title. The view refreshes every 2s; a title changes rarely.
const titleTTL = 30 * time.Second

type cachedTitle struct {
	title string
	at    time.Time
}

var (
	titleMu    sync.Mutex
	titleCache = map[string]cachedTitle{}
)

// sessionTitle returns the title of a session file in Claude Code's
// order (custom title, AI title, summary), cached for titleTTL. It
// returns "" when the file has none.
func sessionTitle(path string, now time.Time) string {
	titleMu.Lock()
	c, ok := titleCache[path]
	titleMu.Unlock()
	if ok && now.Sub(c.at) < titleTTL {
		return c.title
	}
	var title string
	if m, err := session.ReadMeta(path); err == nil {
		title = m.Title()
	}
	titleMu.Lock()
	titleCache[path] = cachedTitle{title: title, at: now}
	titleMu.Unlock()
	return title
}

// activeTickMsg is the 2s heartbeat for the :active view. Carries no
// payload — on receipt we fire a fresh load and schedule the next tick.
type activeTickMsg struct{}

func activeTickCmd() tea.Cmd {
	return tea.Tick(activeRefreshInterval, func(time.Time) tea.Msg { return activeTickMsg{} })
}

// focusDoneMsg carries the outcome of a focus.ByUUID call so the TUI
// can surface a status-line message on failure.
type focusDoneMsg struct {
	uuid string
	err  error
}

// focusCmd runs focus.ByUUID in a goroutine; the TUI keeps rendering.
func focusCmd(uuid string) tea.Cmd {
	return func() tea.Msg {
		return focusDoneMsg{uuid: uuid, err: focus.ByUUID(uuid)}
	}
}

// resumeDoneMsg carries the outcome of a `claude --resume` run. The
// TUI reloads the active list on receipt so the row's state flips to
// running after a successful resume.
type resumeDoneMsg struct {
	uuid string
	err  error
}

// resumeCmd suspends the TUI, runs `claude --resume <uuid>` with its
// standard streams attached, and returns the TUI on exit. Cwd must
// be the real path the session belongs to — otherwise claude starts
// a new session in the current cwd instead of resuming.
func resumeCmd(uuid, cwd string) tea.Cmd {
	c := exec.Command("claude", "--resume", uuid)
	c.Dir = cwd
	c.Env = os.Environ()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return resumeDoneMsg{uuid: uuid, err: err}
	})
}

// archiveDoneMsg carries the result of an archive attempt so the TUI
// can surface outcome + reload the sessions list.
type archiveDoneMsg struct {
	uuid string
	path string // archive path on success
	err  error
}

func archiveCmd(uuid string) tea.Cmd {
	return func() tea.Msg {
		res, err := archive.ArchiveSession(uuid)
		return archiveDoneMsg{uuid: uuid, path: res.ArchivePath, err: err}
	}
}

// restoreDoneMsg carries the result of a restore attempt.
type restoreDoneMsg struct {
	uuid string
	path string // restored jsonl path on success
	err  error
}

func restoreCmd(uuid string) tea.Cmd {
	return func() tea.Msg {
		res, err := archive.RestoreSession(uuid)
		return restoreDoneMsg{uuid: uuid, path: res.JSONLPath, err: err}
	}
}
