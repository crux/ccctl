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
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	active        []proc.Info
	activeCursor  int
	activeLoading bool
	activeTicking bool
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
		m.active = msg.procs
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
	return loadSessionsCmd(dir)
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
