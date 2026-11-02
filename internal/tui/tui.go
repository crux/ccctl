// Package tui hosts the Bubble Tea application behind `ccctl ui`.
//
// Step 2 scaffold: three resource views (projects, sessions, active) with
// k9s-style modal navigation (`:projects`, `:sessions`, `:active` or 1/2/3)
// and a two-column + wide-detail layout for the browse flow. PROJECTS and
// SESSIONS panes carry real data; the DETAIL pane reads from whichever
// column currently has focus. Live refresh and resume actions arrive in
// later steps.
package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/crux/ccctl/internal/focus"
	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
)

type view int

const (
	viewProjects view = iota
	viewSessions
	viewActive
)

func (v view) label() string {
	switch v {
	case viewProjects:
		return "projects"
	case viewSessions:
		return "sessions"
	case viewActive:
		return "active"
	}
	return "?"
}

// Model is the top-level Bubble Tea model. The focused browse column
// equals m.view (viewProjects → PROJECTS pane focused; viewSessions →
// SESSIONS pane focused). The DETAIL pane reads from whichever column
// is focused.
type Model struct {
	view   view
	width  int
	height int

	cmdMode bool
	cmdBuf  string
	status  string

	// Data + cursors
	includeTmp      bool
	includeArchived bool // when true, .zip rows appear in the sessions pane
	projects        []project.Info
	projectCursor   int
	projectsLoading bool
	projectsLoaded  bool
	sessions        []session.Row
	sessionsFor     string // the project.Dir the current sessions slice belongs to
	sessionCursor   int
	sessionsLoading bool
	activeSet       proc.ActiveSet
	activeLoaded    bool

	// :active view state. active is the full enumerated list of running
	// claude processes; activeCursor selects a row. activeLoading flips
	// while a load is in flight. activeTicking is true when the view is
	// currently receiving 2s refreshes — gated so no work happens while
	// the user is elsewhere.
	active            []proc.Row
	activeCursor      int
	activeLoading     bool
	activeTicking     bool
	activeLastRefresh time.Time

	// Split ratios, percent of the available body region.
	// topRowPct   — height of PROJECTS/SESSIONS row (vs DETAIL below).
	// projColPct  — width of PROJECTS within the top row (vs SESSIONS).
	// Both are clamped to [splitMin, splitMax] on every nudge so a
	// pane never collapses below readability.
	topRowPct  int
	projColPct int
}

const (
	splitMin = 20
	splitMax = 80
)

// New returns an initial Model with the projects view selected.
func New() Model {
	return Model{
		view:            viewProjects,
		projectsLoading: true,
		topRowPct:       65,
		projColPct:      40,
	}
}

// Run starts the Bubble Tea program. Blocks until the user quits.
func Run() error {
	p := tea.NewProgram(New(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadProjectsCmd(m.includeTmp),
		loadActiveCmd(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case projectsLoadedMsg:
		m.projectsLoading = false
		m.projectsLoaded = true
		if msg.err != nil {
			m.status = "projects: " + msg.err.Error()
			return m, nil
		}
		m.projects = msg.projects
		if m.projectCursor >= len(m.projects) {
			m.projectCursor = 0
		}
		return m, m.loadSessionsForCursor()

	case sessionsLoadedMsg:
		// Drop stale loads whose project no longer matches our cursor.
		if m.currentProjectDir() != msg.projectDir {
			return m, nil
		}
		m.sessionsLoading = false
		m.sessionsFor = msg.projectDir
		if msg.err != nil {
			m.sessions = nil
			m.status = "sessions: " + msg.err.Error()
			return m, nil
		}
		m.sessions = msg.rows
		if m.sessionCursor >= len(m.sessions) {
			m.sessionCursor = 0
		}
		return m, nil

	case activeLoadedMsg:
		m.activeLoaded = true
		m.activeSet = msg.set
		return m, nil

	case activeListLoadedMsg:
		m.activeLoading = false
		m.activeLastRefresh = time.Now()
		if msg.err != nil {
			m.status = "active: " + msg.err.Error()
			return m, nil
		}
		m.active = msg.rows
		if m.activeCursor >= len(m.active) {
			if len(m.active) == 0 {
				m.activeCursor = 0
			} else {
				m.activeCursor = len(m.active) - 1
			}
		}
		return m, nil

	case activeTickMsg:
		// Tick is scoped to :active. If we're elsewhere, let the tick
		// die quietly — a new one spins up on next entry.
		if m.view != viewActive {
			m.activeTicking = false
			return m, nil
		}
		return m, tea.Batch(loadActiveListCmd(), activeTickCmd())

	case focusDoneMsg:
		var bg *focus.BackgroundError
		if errors.As(msg.err, &bg) {
			return m, attachCmd(msg.uuid, bg.ID)
		}
		if msg.err != nil {
			m.status = fmt.Sprintf("focus %s: %v", shortUUID(msg.uuid), msg.err)
		} else {
			m.status = fmt.Sprintf("focused %s", shortUUID(msg.uuid))
		}
		return m, nil

	case archiveDoneMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("archive %s: %v", shortUUID(msg.uuid), msg.err)
			return m, nil
		}
		m.status = fmt.Sprintf("archived %s", shortUUID(msg.uuid))
		return m, m.reloadSessions()

	case restoreDoneMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("restore %s: %v", shortUUID(msg.uuid), msg.err)
			return m, nil
		}
		m.status = fmt.Sprintf("restored %s", shortUUID(msg.uuid))
		return m, m.reloadSessions()

	case resumeDoneMsg:
		// On return from `claude --resume`, refresh everything that
		// might have changed while the TUI was suspended: the active
		// set (for markers) and the active list (if we're in :active).
		cmds := []tea.Cmd{loadActiveCmd()}
		if m.view == viewActive {
			cmds = append(cmds, loadActiveListCmd())
		}
		// The session that we just left may have grown; reload it too.
		if cmd := m.reloadSessions(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		switch {
		case msg.err != nil && msg.attach:
			m.status = fmt.Sprintf("attach %s: %v", shortUUID(msg.uuid), msg.err)
		case msg.err != nil:
			m.status = fmt.Sprintf("resume %s: %v", shortUUID(msg.uuid), msg.err)
		case msg.attach:
			m.status = fmt.Sprintf("back from claude attach %s — it keeps running in the background", shortUUID(msg.uuid))
		default:
			m.status = fmt.Sprintf("resumed %s — back from claude", shortUUID(msg.uuid))
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		if m.cmdMode {
			return m.updateCmdMode(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl-c":
		return m, tea.Quit
	case ":":
		m.cmdMode = true
		m.cmdBuf = ""
		m.status = ""
		return m, nil

	// View / column switching
	case "1":
		return m.switchView(viewProjects)
	case "2":
		return m.switchView(viewSessions)
	case "3":
		return m.switchView(viewActive)
	case "tab":
		if m.view == viewProjects {
			return m.switchView(viewSessions)
		}
		if m.view == viewSessions {
			return m.switchView(viewProjects)
		}
	case "h":
		if m.view == viewSessions {
			return m.switchView(viewProjects)
		}
	case "l":
		if m.view == viewProjects {
			return m.switchView(viewSessions)
		}

	// Cursor movement within the focused list
	case "up", "k":
		return m.moveCursor(-1)
	case "down", "j":
		return m.moveCursor(+1)
	case "g", "home":
		return m.jumpCursor(0)
	case "G", "end":
		return m.jumpCursor(-1) // -1 → last

	// Enter: context-dependent action per view.
	case "enter":
		return m.enterAction()

	// Session-row actions (`:sessions` only).
	case "A":
		return m.toggleIncludeArchived()
	case "a":
		return m.archiveAction()
	case "r":
		return m.restoreAction()

	// Pane resize. +/- grow or shrink the top row; </> shift the
	// PROJECTS vs SESSIONS boundary within the top row.
	case "+", "=":
		m.topRowPct = clamp(m.topRowPct+5, splitMin, splitMax)
		return m, nil
	case "-", "_":
		m.topRowPct = clamp(m.topRowPct-5, splitMin, splitMax)
		return m, nil
	case ">", ".":
		m.projColPct = clamp(m.projColPct+5, splitMin, splitMax)
		return m, nil
	case "<", ",":
		m.projColPct = clamp(m.projColPct-5, splitMin, splitMax)
		return m, nil
	}
	return m, nil
}

func (m Model) updateCmdMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.cmdMode = false
		m.cmdBuf = ""
		return m, nil
	case "enter":
		cmd := strings.TrimSpace(m.cmdBuf)
		m.cmdMode = false
		m.cmdBuf = ""
		return m.runCommand(cmd)
	case "backspace":
		if len(m.cmdBuf) > 0 {
			m.cmdBuf = m.cmdBuf[:len(m.cmdBuf)-1]
		}
		return m, nil
	default:
		s := msg.String()
		if len(s) == 1 {
			m.cmdBuf += s
		}
		return m, nil
	}
}

// runCommand interprets a `:…` command. Unknown commands land in the
// status line.
func (m Model) runCommand(cmd string) (tea.Model, tea.Cmd) {
	switch cmd {
	case "projects":
		return m.switchView(viewProjects)
	case "sessions":
		return m.switchView(viewSessions)
	case "active":
		return m.switchView(viewActive)
	case "q", "quit":
		m.status = "use 'q' in normal mode to quit"
		return m, nil
	case "":
		m.status = ""
		return m, nil
	default:
		m.status = fmt.Sprintf("unknown command: :%s", cmd)
		return m, nil
	}
}

// enterAction routes the Enter key per view:
//
//	:projects → switch to :sessions (next natural step in the drill-down)
//	:sessions → focus if the session has a running process, resume via
//	            tea.ExecProcess if not, status-hint if archived
//	:active   → focus the row's UUID (every row is running by definition);
//	            a background session that no terminal shows is attached
//	            here; a helper row of the daemon gets a hint
func (m Model) enterAction() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewProjects:
		return m.switchView(viewSessions)

	case viewSessions:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessions) {
			return m, nil
		}
		r := m.sessions[m.sessionCursor]
		if r.Archived {
			m.status = fmt.Sprintf("archived — restore first: ccctl restore %s", r.UUID)
			return m, nil
		}
		if m.activeSet.Confident[r.UUID] || m.activeSet.Guessed[r.UUID] {
			m.status = fmt.Sprintf("focusing %s…", shortUUID(r.UUID))
			return m, focusCmd(r.UUID)
		}
		cwd := m.resolveResumeCwd(r)
		if cwd == "" {
			m.status = fmt.Sprintf("no cwd known for %s; resume manually (cd <dir> && claude --resume %s)", shortUUID(r.UUID), r.UUID)
			return m, nil
		}
		return m, resumeCmd(r.UUID, cwd)

	case viewActive:
		if m.activeCursor < 0 || m.activeCursor >= len(m.active) {
			return m, nil
		}
		p := m.active[m.activeCursor]
		switch {
		case p.AttachTarget() != "":
			m.status = fmt.Sprintf("focusing %s…", p.AttachTarget())
			return m, focusPIDCmd(p.PID)
		case p.Role != proc.RoleSession:
			m.status = helperHint(p)
			return m, nil
		}
		if p.UUID == "" {
			m.status = fmt.Sprintf("no uuid known for pid %d; cannot focus", p.PID)
			return m, nil
		}
		m.status = fmt.Sprintf("focusing %s…", shortUUID(p.UUID))
		return m, focusCmd(p.UUID)
	}
	return m, nil
}

// toggleIncludeArchived flips whether .zip rows show in the sessions
// pane and reloads the current project's sessions. Only meaningful in
// :sessions / :projects; silent elsewhere.
func (m Model) toggleIncludeArchived() (tea.Model, tea.Cmd) {
	if m.view != viewProjects && m.view != viewSessions {
		return m, nil
	}
	m.includeArchived = !m.includeArchived
	label := "hidden"
	if m.includeArchived {
		label = "shown"
	}
	m.status = fmt.Sprintf("archived: %s", label)
	return m, m.reloadSessions()
}

// archiveAction archives the session under the sessions cursor. Only
// meaningful in :sessions. Refuses active or already-archived rows.
func (m Model) archiveAction() (tea.Model, tea.Cmd) {
	if m.view != viewSessions {
		return m, nil
	}
	if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessions) {
		return m, nil
	}
	r := m.sessions[m.sessionCursor]
	if r.Archived {
		m.status = fmt.Sprintf("already archived: %s", shortUUID(r.UUID))
		return m, nil
	}
	if m.activeSet.Confident[r.UUID] || m.activeSet.Guessed[r.UUID] {
		m.status = fmt.Sprintf("active — cannot archive %s", shortUUID(r.UUID))
		return m, nil
	}
	m.status = fmt.Sprintf("archiving %s…", shortUUID(r.UUID))
	return m, archiveCmd(r.UUID)
}

// restoreAction restores the archived session under the sessions
// cursor. Only meaningful in :sessions on a .zip row.
func (m Model) restoreAction() (tea.Model, tea.Cmd) {
	if m.view != viewSessions {
		return m, nil
	}
	if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessions) {
		return m, nil
	}
	r := m.sessions[m.sessionCursor]
	if !r.Archived {
		m.status = fmt.Sprintf("not archived: %s", shortUUID(r.UUID))
		return m, nil
	}
	m.status = fmt.Sprintf("restoring %s…", shortUUID(r.UUID))
	return m, restoreCmd(r.UUID)
}

// resolveResumeCwd finds the real working directory to run
// `claude --resume` from. Prefers the session's own last-known cwd
// (last-wins from the .jsonl), falling back to the project's decoded
// cwd. Returns "" when neither is available.
func (m Model) resolveResumeCwd(r session.Row) string {
	if r.Meta.Cwd != "" {
		return r.Meta.Cwd
	}
	if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor].Cwd
	}
	return ""
}

func shortUUID(u string) string {
	if len(u) >= 8 {
		return u[:8]
	}
	return u
}

// switchView moves to v. Entering :active kicks off an immediate load
// and starts the 2s refresh tick if one isn't already running. The
// tick naturally dies when the user leaves :active.
func (m Model) switchView(v view) (tea.Model, tea.Cmd) {
	prev := m.view
	m.view = v
	if v == viewActive && prev != viewActive {
		var cmds []tea.Cmd
		m.activeLoading = true
		cmds = append(cmds, loadActiveListCmd())
		if !m.activeTicking {
			m.activeTicking = true
			cmds = append(cmds, activeTickCmd())
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// moveCursor shifts the focused list's cursor by delta and, if that
// changes the project under focus, triggers a session reload.
func (m Model) moveCursor(delta int) (Model, tea.Cmd) {
	switch m.view {
	case viewProjects:
		if len(m.projects) == 0 {
			return m, nil
		}
		m.projectCursor = clamp(m.projectCursor+delta, 0, len(m.projects)-1)
		return m, m.loadSessionsForCursor()
	case viewSessions:
		if len(m.sessions) == 0 {
			return m, nil
		}
		m.sessionCursor = clamp(m.sessionCursor+delta, 0, len(m.sessions)-1)
		return m, nil
	case viewActive:
		if len(m.active) == 0 {
			return m, nil
		}
		m.activeCursor = clamp(m.activeCursor+delta, 0, len(m.active)-1)
		return m, nil
	}
	return m, nil
}

func (m Model) jumpCursor(target int) (Model, tea.Cmd) {
	switch m.view {
	case viewProjects:
		if len(m.projects) == 0 {
			return m, nil
		}
		if target < 0 {
			target = len(m.projects) - 1
		}
		m.projectCursor = clamp(target, 0, len(m.projects)-1)
		return m, m.loadSessionsForCursor()
	case viewSessions:
		if len(m.sessions) == 0 {
			return m, nil
		}
		if target < 0 {
			target = len(m.sessions) - 1
		}
		m.sessionCursor = clamp(target, 0, len(m.sessions)-1)
		return m, nil
	case viewActive:
		if len(m.active) == 0 {
			return m, nil
		}
		if target < 0 {
			target = len(m.active) - 1
		}
		m.activeCursor = clamp(target, 0, len(m.active)-1)
		return m, nil
	}
	return m, nil
}

// loadSessionsForCursor returns a Cmd that loads sessions for whichever
// project is under the projects cursor. Returns nil when the sessions
// slice already matches that project.
func (m *Model) loadSessionsForCursor() tea.Cmd {
	dir := m.currentProjectDir()
	if dir == "" {
		return nil
	}
	if m.sessionsFor == dir && !m.sessionsLoading {
		return nil
	}
	m.sessionsLoading = true
	m.sessionCursor = 0
	return loadSessionsCmd(dir, m.includeArchived)
}

// reloadSessions forces a reload of the current project's sessions,
// bypassing the sessionsFor shortcut. Used after a mutation (archive /
// restore) or a toggle flip changed what should be in the list.
func (m *Model) reloadSessions() tea.Cmd {
	dir := m.currentProjectDir()
	if dir == "" {
		return nil
	}
	m.sessionsLoading = true
	m.sessionsFor = ""
	return loadSessionsCmd(dir, m.includeArchived)
}

// currentProjectDir returns the Dir of the project under the cursor, or
// "" when the project list is empty.
func (m Model) currentProjectDir() string {
	if len(m.projects) == 0 {
		return ""
	}
	if m.projectCursor < 0 || m.projectCursor >= len(m.projects) {
		return ""
	}
	return m.projects[m.projectCursor].Dir
}

// helperHint explains a helper row of the daemon in the status line.
func helperHint(r proc.Row) string {
	switch r.Role {
	case proc.RoleHost:
		return fmt.Sprintf("host %d: %s — Enter on the session above opens it", r.PID, r.Note)
	case proc.RoleDaemon:
		return fmt.Sprintf("daemon %d runs the background sessions; it ends after the last one", r.PID)
	case proc.RoleSpare:
		return fmt.Sprintf("spare %d: the next claude --bg starts in it", r.PID)
	}
	return fmt.Sprintf("pid %d: %s", r.PID, r.Note)
}
