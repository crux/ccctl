// Package tui hosts the Bubble Tea application behind `ccctl ui`.
//
// Step 1 scaffold: three resource views (projects, sessions, active) with
// k9s-style modal navigation (`:projects`, `:sessions`, `:active` or 1/2/3)
// and a two-column + wide-detail layout for the browse flow. Data loading,
// live refresh, and resume actions arrive in later steps.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

// Model is the top-level Bubble Tea model.
type Model struct {
	view    view
	width   int
	height  int
	cmdMode bool
	cmdBuf  string
	status  string
}

// New returns an initial Model with the projects view selected.
func New() Model {
	return Model{view: viewProjects}
}

// Run starts the Bubble Tea program. Blocks until the user quits.
func Run() error {
	p := tea.NewProgram(New(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
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
	case "1":
		m.view = viewProjects
		return m, nil
	case "2":
		m.view = viewSessions
		return m, nil
	case "3":
		m.view = viewActive
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
		return m.runCommand(cmd), nil
	case "backspace":
		if len(m.cmdBuf) > 0 {
			m.cmdBuf = m.cmdBuf[:len(m.cmdBuf)-1]
		}
		return m, nil
	default:
		// Append printable keys; ignore control sequences.
		s := msg.String()
		if len(s) == 1 {
			m.cmdBuf += s
		}
		return m, nil
	}
}

// runCommand interprets a `:…` command. Unknown commands land in the status
// line so the UI stays responsive and self-explanatory.
func (m Model) runCommand(cmd string) Model {
	switch cmd {
	case "projects":
		m.view = viewProjects
	case "sessions":
		m.view = viewSessions
	case "active":
		m.view = viewActive
	case "q", "quit":
		// Can't return tea.Quit from here; set a flag the caller
		// could watch — but for simplicity, the command mode's own
		// enter handler takes the plain path. 'q' already quits in
		// normal mode. Fall through to unknown-command for now.
		m.status = "use 'q' in normal mode to quit"
	case "":
		// Empty :<enter> is a no-op; clear status.
		m.status = ""
	default:
		m.status = fmt.Sprintf("unknown command: :%s", cmd)
	}
	return m
}
