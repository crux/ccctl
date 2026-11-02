package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
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

// activeListLoadedMsg carries the full enumerated active-process list,
// arranged for display (helper processes under their session or daemon).
type activeListLoadedMsg struct {
	rows []proc.Row
	err  error
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
		return activeListLoadedMsg{rows: proc.Arrange(procs), err: err}
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
// For a background session that no terminal shows, focus.ByUUID returns
// a *focus.BackgroundError, and Update attaches it here instead.
func focusCmd(uuid string) tea.Cmd {
	return func() tea.Msg {
		return focusDoneMsg{uuid: uuid, err: focus.ByUUID(uuid)}
	}
}

// focusPIDCmd brings the terminal of a process to the front, e.g. the
// terminal where a `claude attach` shows a background session.
func focusPIDCmd(pid int) tea.Cmd {
	return func() tea.Msg {
		return focusDoneMsg{uuid: fmt.Sprintf("pid %d", pid), err: focus.ByPID(pid)}
	}
}

// resumeDoneMsg carries the outcome of a `claude --resume` or `claude
// attach` run. The TUI reloads the active list on receipt so the row's
// state flips to running after a successful resume.
type resumeDoneMsg struct {
	uuid   string
	attach bool
	err    error
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

// attachCmd suspends the TUI and runs `claude attach <id>`, which shows
// a background session in this terminal. The session keeps running in
// the background when attach ends; the TUI comes back then.
func attachCmd(uuid, id string) tea.Cmd {
	c := exec.Command("claude", "attach", id)
	c.Env = os.Environ()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return resumeDoneMsg{uuid: uuid, attach: true, err: err}
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

// AllRow is one row for the :all cross-project view. Wraps session.Row
// with the owning project's display name (abbreviated path when known,
// encoded namespace basename otherwise).
type AllRow struct {
	session.Row
	ProjectName string
	ProjectDir  string // real project.Dir under ~/.claude/projects/
}

type allLoadedMsg struct {
	rows []AllRow
	err  error
}

// loadAllCmd enumerates every ~/.claude/projects/ dir with the same
// tmp-exclusion rule as :projects, then fans out ListInProject with
// bounded concurrency and flattens the result into one []AllRow.
func loadAllCmd(includeTmp, includeArchived bool) tea.Cmd {
	return func() tea.Msg {
		projects, err := project.ListProjects()
		if err != nil {
			return allLoadedMsg{err: err}
		}
		home, _ := os.UserHomeDir()
		type pending struct {
			dir  string
			name string
		}
		var kept []pending
		for _, p := range projects {
			if !includeTmp && project.IsTmp(p) {
				continue
			}
			name := p.EncodedName
			if p.Cwd != "" {
				name = p.Cwd
				if home != "" && strings.HasPrefix(name, home) {
					name = "~" + strings.TrimPrefix(name, home)
				}
			}
			kept = append(kept, pending{dir: p.Dir, name: name})
		}

		type partial struct {
			rows []session.Row
			name string
			dir  string
		}
		results := make([]partial, len(kept))
		const workers = 8
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for i, p := range kept {
			wg.Add(1)
			go func(i int, dir, name string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				rows, _ := session.ListInProject(dir, includeArchived)
				results[i] = partial{rows: rows, name: name, dir: dir}
			}(i, p.dir, p.name)
		}
		wg.Wait()

		var all []AllRow
		for _, r := range results {
			for _, row := range r.rows {
				all = append(all, AllRow{Row: row, ProjectName: r.name, ProjectDir: r.dir})
			}
		}
		return allLoadedMsg{rows: all}
	}
}
