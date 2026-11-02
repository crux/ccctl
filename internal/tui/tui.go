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
	"os"
	"sort"
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
	viewAll
)

func (v view) label() string {
	switch v {
	case viewProjects:
		return "projects"
	case viewSessions:
		return "sessions"
	case viewActive:
		return "active"
	case viewAll:
		return "all"
	}
	return "?"
}

// allSortKey picks which column drives the sort in :all. Direction is
// held separately via allSortDesc so we can reverse any key.
type allSortKey int

const (
	allSortMTime allSortKey = iota // default
	allSortTitle
	allSortSize
	allSortProject
	allSortUUID
)

func (k allSortKey) label() string {
	switch k {
	case allSortMTime:
		return "mtime"
	case allSortTitle:
		return "title"
	case allSortSize:
		return "size"
	case allSortProject:
		return "project"
	case allSortUUID:
		return "uuid"
	}
	return "?"
}

// naturalDesc gives the sort direction most people expect for a given
// key: mtime newest first, size biggest first; title and project
// alphabetically ascending.
func naturalDesc(k allSortKey) bool {
	switch k {
	case allSortMTime, allSortSize:
		return true
	}
	return false
}

// allStatus filters the :all list to one slice of the running / archived
// axis. Cycled by `f`.
type allStatus int

const (
	allStatusAll allStatus = iota
	allStatusRunning
	allStatusNotRunning
	allStatusArchived
)

func (s allStatus) label() string {
	switch s {
	case allStatusAll:
		return "all"
	case allStatusRunning:
		return "running"
	case allStatusNotRunning:
		return "not-running"
	case allStatusArchived:
		return "archived"
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

	// :all view state. all is the full cross-project session list,
	// loaded once per entry (and after mutations) via loadAllCmd. The
	// filtered + sorted view is computed on each render via
	// allVisible() so cursor movement, filter edits and sort changes
	// stay cheap and the raw data stays canonical.
	all             []AllRow
	allCursor       int
	allLoading      bool
	allLoaded       bool
	allLastRefresh  time.Time
	allSort         allSortKey
	allSortDesc     bool
	allStatusFilter allStatus
	filterMode      bool   // typing into the /… input
	filterBuf       string // live filter text, incremental

	// peek is the Space quick-look overlay (rung 5). peeking is true
	// when the overlay is drawn on top of the current view; peekRow is
	// the session row it shows; peekTurns carries the first + last
	// user-turn previews, loaded async via loadPeekTurnsCmd.
	peeking     bool
	peekRow     session.Row
	peekTurns   session.Turns
	peekLoaded  bool
	peekLoadFor string // uuid whose turn-load is in flight; drops stale replies

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
		allSort:         allSortMTime,
		allSortDesc:     naturalDesc(allSortMTime),
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
		m.clearRefreshingStatus()
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
		m.clearRefreshingStatus()
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
		m.clearRefreshingStatus()
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

	case allLoadedMsg:
		m.allLoading = false
		m.allLoaded = true
		m.allLastRefresh = time.Now()
		if msg.err != nil {
			m.status = "all: " + msg.err.Error()
			return m, nil
		}
		m.clearRefreshingStatus()
		m.all = msg.rows
		m.clampAllCursor()
		return m, nil

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
		return m, m.reloadAfterSessionMutation()

	case restoreDoneMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("restore %s: %v", shortUUID(msg.uuid), msg.err)
			return m, nil
		}
		m.status = fmt.Sprintf("restored %s", shortUUID(msg.uuid))
		return m, m.reloadAfterSessionMutation()

	case resumeDoneMsg:
		// On return from `claude --resume`, refresh everything that
		// might have changed while the TUI was suspended: the active
		// set (for markers) and the active list (if we're in :active).
		cmds := []tea.Cmd{loadActiveCmd()}
		if m.view == viewActive {
			cmds = append(cmds, loadActiveListCmd())
		}
		// The session we just left may have grown; reload whichever
		// view currently cares about session rows.
		if cmd := m.reloadAfterSessionMutation(); cmd != nil {
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
		if m.filterMode {
			return m.updateFilterMode(msg)
		}
		if m.peeking {
			return m.updatePeekMode(msg)
		}
		return m.updateNormal(msg)

	case peekTurnsLoadedMsg:
		// Drop stale replies: cursor may have moved to a different row
		// (or the overlay closed) while the read was in flight.
		if !m.peeking || m.peekRow.UUID != msg.uuid {
			return m, nil
		}
		m.peekLoaded = true
		m.peekTurns = msg.turns
		return m, nil
	}
	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
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
	case "4":
		return m.switchView(viewAll)
	case "tab":
		if m.view == viewProjects {
			return m.switchView(viewSessions)
		}
		if m.view == viewSessions {
			return m.switchView(viewProjects)
		}
	case "h", "left":
		if m.view == viewAll {
			return m.cycleSort(-1)
		}
		if m.view == viewSessions {
			return m.switchView(viewProjects)
		}
	case "l", "right":
		if m.view == viewAll {
			return m.cycleSort(+1)
		}
		if m.view == viewProjects {
			return m.switchView(viewSessions)
		}

	// Refresh the current view off-cycle.
	case "ctrl+r":
		return m.refreshCurrentView()

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

	// Space: non-destructive quick-look peek on the row under the cursor
	// in :sessions and :all. Elsewhere, Space is a no-op.
	case " ":
		return m.openPeek()

	// Session-row actions (`:sessions` and `:all`).
	case "A":
		return m.toggleIncludeArchived()
	case "a":
		return m.archiveAction()
	case "r":
		return m.restoreAction()

	// :all sort + filter keys. s cycles the sort key forward (←/→ are
	// aliases in this view); S reverses direction; f cycles the status
	// filter; / opens the filter input. Each sort-key change resets to
	// that key's natural direction (newest/biggest first for mtime+size,
	// alphabetical for title+project) — S from there reverses.
	case "s":
		if m.view == viewAll {
			return m.cycleSort(+1)
		}
	case "S":
		if m.view == viewAll {
			m.allSortDesc = !m.allSortDesc
			m.status = fmt.Sprintf("sort: %s%s", m.allSort.label(), sortDirMark(m.allSortDesc))
			return m, nil
		}
	case "f":
		if m.view == viewAll {
			m.allStatusFilter = (m.allStatusFilter + 1) % 4
			m.allCursor = 0
			m.status = fmt.Sprintf("status filter: %s", m.allStatusFilter.label())
			return m, nil
		}
	case "/":
		if m.view == viewAll {
			m.filterMode = true
			m.status = ""
			return m, nil
		}

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

// updatePeekMode routes keypresses while the Space quick-look overlay
// is open. Space or Esc closes; every other key is ignored so the
// overlay doesn't accidentally fire an action on the row underneath.
func (m Model) updatePeekMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case " ", "esc", "q":
		m.peeking = false
		m.peekLoaded = false
		m.peekTurns = session.Turns{}
		return m, nil
	}
	return m, nil
}

// openPeek snapshots the row under the cursor and opens the quick-look
// overlay. No-op in views that don't have session rows (:projects,
// :active). Fires an async turn-tail load; the metadata is already in
// hand so the overlay paints immediately with "(reading…)" where the
// turn tails will land.
func (m Model) openPeek() (tea.Model, tea.Cmd) {
	r, ok := m.sessionRowForAction()
	if !ok {
		return m, nil
	}
	m.peeking = true
	m.peekRow = r
	m.peekTurns = session.Turns{}
	m.peekLoaded = false
	m.peekLoadFor = r.UUID
	// Archived rows carry their turns inside the .zip; defer that path
	// until it's useful.
	if r.Archived {
		m.peekLoaded = true
		return m, nil
	}
	return m, loadPeekTurnsCmd(r.UUID, r.Path)
}

// updateFilterMode routes keypresses while the /… filter input is active
// in :all. The filter is incremental: every keystroke that changes
// filterBuf resets the cursor so the user doesn't drift off a now-
// filtered row.
func (m Model) updateFilterMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterMode = false
		m.filterBuf = ""
		m.allCursor = 0
		return m, nil
	case "enter":
		m.filterMode = false
		return m, nil
	case "backspace":
		if len(m.filterBuf) > 0 {
			m.filterBuf = m.filterBuf[:len(m.filterBuf)-1]
			m.allCursor = 0
		}
		return m, nil
	default:
		s := msg.String()
		if len(s) == 1 {
			m.filterBuf += s
			m.allCursor = 0
		}
		return m, nil
	}
}

// clearRefreshingStatus clears the "refreshing…" status set by
// refreshCurrentView once a load completes, without touching any other
// transient status message the user may have in flight.
func (m *Model) clearRefreshingStatus() {
	if m.status == "refreshing…" {
		m.status = ""
	}
}

// sortDirMark is a short glyph showing current sort direction.
func sortDirMark(desc bool) string {
	if desc {
		return " ↓"
	}
	return " ↑"
}

// allSortOrder is the sortable columns in the order they appear on
// screen in :all. Left/right (and s) walk this list so stepping tracks
// what the eye sees rather than the enum's declaration order.
var allSortOrder = []allSortKey{allSortMTime, allSortSize, allSortProject, allSortUUID, allSortTitle}

// cycleSort shifts the :all sort key by delta — in visual column order —
// and resets to the key's natural direction. Pressed as `s` (+1),
// `h`/`←` (-1), `l`/`→` (+1).
func (m Model) cycleSort(delta int) (tea.Model, tea.Cmd) {
	n := len(allSortOrder)
	idx := 0
	for i, k := range allSortOrder {
		if k == m.allSort {
			idx = i
			break
		}
	}
	idx = ((idx+delta)%n + n) % n
	m.allSort = allSortOrder[idx]
	m.allSortDesc = naturalDesc(m.allSort)
	m.allCursor = 0
	m.status = fmt.Sprintf("sort: %s%s", m.allSort.label(), sortDirMark(m.allSortDesc))
	return m, nil
}

// allVisible returns the indices into m.all that pass the current
// filters, in the configured sort order. Computed on every call; m.all
// stays canonical. Cheap at the scale we care about (~hundreds of rows).
func (m Model) allVisible() []int {
	home, _ := os.UserHomeDir()
	_ = home // unused here; name resolution happens in loadAllCmd
	var out []int
	needle := strings.ToLower(m.filterBuf)
	for i, r := range m.all {
		if !m.allRowMatchesStatus(r) {
			continue
		}
		if needle != "" && !allRowMatchesText(r, needle) {
			continue
		}
		out = append(out, i)
	}
	sort.SliceStable(out, func(a, b int) bool {
		return allLess(m.all[out[a]], m.all[out[b]], m.allSort, m.allSortDesc)
	})
	return out
}

// allRowMatchesStatus honours the current cycle of f: all / running /
// not-running / archived.
func (m Model) allRowMatchesStatus(r AllRow) bool {
	switch m.allStatusFilter {
	case allStatusRunning:
		return m.activeSet.Confident[r.UUID] || m.activeSet.Guessed[r.UUID]
	case allStatusNotRunning:
		if r.Archived {
			return false
		}
		return !m.activeSet.Confident[r.UUID] && !m.activeSet.Guessed[r.UUID]
	case allStatusArchived:
		return r.Archived
	}
	return true
}

// allRowMatchesText is the free-text filter — matches on title,
// project name, or UUID prefix. Case-insensitive.
func allRowMatchesText(r AllRow, needle string) bool {
	title := strings.ToLower(r.Meta.Display())
	if strings.Contains(title, needle) {
		return true
	}
	if strings.Contains(strings.ToLower(r.ProjectName), needle) {
		return true
	}
	if strings.HasPrefix(strings.ToLower(r.UUID), needle) {
		return true
	}
	return false
}

// allLess compares two rows for the configured sort key. desc inverts.
// The secondary sort (within equal primary keys) is always mtime desc,
// so a sort by title or project still shows newest first inside a bucket.
func allLess(a, b AllRow, key allSortKey, desc bool) bool {
	primary := func() int {
		switch key {
		case allSortTitle:
			return strings.Compare(strings.ToLower(a.Meta.Display()), strings.ToLower(b.Meta.Display()))
		case allSortSize:
			switch {
			case a.Size < b.Size:
				return -1
			case a.Size > b.Size:
				return 1
			}
			return 0
		case allSortProject:
			return strings.Compare(strings.ToLower(a.ProjectName), strings.ToLower(b.ProjectName))
		case allSortUUID:
			return strings.Compare(a.UUID, b.UUID)
		}
		// Default: mtime
		switch {
		case a.MTime.Before(b.MTime):
			return -1
		case a.MTime.After(b.MTime):
			return 1
		}
		return 0
	}()
	if primary != 0 {
		if desc {
			return primary > 0
		}
		return primary < 0
	}
	// Secondary: newer first regardless of desc.
	return a.MTime.After(b.MTime)
}

func (m *Model) clampAllCursor() {
	vis := m.allVisible()
	if m.allCursor >= len(vis) {
		if len(vis) == 0 {
			m.allCursor = 0
		} else {
			m.allCursor = len(vis) - 1
		}
	}
	if m.allCursor < 0 {
		m.allCursor = 0
	}
}

// currentAllRow returns the AllRow under the :all cursor (or ok=false).
func (m Model) currentAllRow() (AllRow, bool) {
	vis := m.allVisible()
	if m.allCursor < 0 || m.allCursor >= len(vis) {
		return AllRow{}, false
	}
	return m.all[vis[m.allCursor]], true
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
	case "all":
		return m.switchView(viewAll)
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

	case viewAll:
		ar, ok := m.currentAllRow()
		if !ok {
			return m, nil
		}
		r := ar.Row
		if r.Archived {
			m.status = fmt.Sprintf("archived — restore first: ccctl restore %s", r.UUID)
			return m, nil
		}
		if m.activeSet.Confident[r.UUID] || m.activeSet.Guessed[r.UUID] {
			m.status = fmt.Sprintf("focusing %s…", shortUUID(r.UUID))
			return m, focusCmd(r.UUID)
		}
		cwd := r.Meta.Cwd
		if cwd == "" {
			m.status = fmt.Sprintf("no cwd known for %s; resume manually (cd <dir> && claude --resume %s)", shortUUID(r.UUID), r.UUID)
			return m, nil
		}
		return m, resumeCmd(r.UUID, cwd)
	}
	return m, nil
}

// refreshCurrentView reloads the data for whichever view is in focus,
// off-cycle. :projects reloads the project list (and the sessions pane
// follows via the cursor matcher); :sessions reloads the current
// project's sessions; :active fires an off-cycle active-list load; the
// 2 s tick continues to run on top. In every view the active-set is
// refreshed too so markers stay honest.
func (m Model) refreshCurrentView() (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{loadActiveCmd()}
	switch m.view {
	case viewProjects:
		m.projectsLoading = true
		cmds = append(cmds, loadProjectsCmd(m.includeTmp))
	case viewSessions:
		if cmd := m.reloadSessions(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case viewActive:
		m.activeLoading = true
		cmds = append(cmds, loadActiveListCmd())
	case viewAll:
		m.allLoading = true
		cmds = append(cmds, loadAllCmd(m.includeTmp, m.includeArchived))
	}
	m.status = "refreshing…"
	return m, tea.Batch(cmds...)
}

// toggleIncludeArchived flips whether .zip rows show in session lists
// and reloads whichever list is current. In :sessions / :projects this
// re-fires the project-level sessions load; in :all this re-fires the
// cross-project load. Silent elsewhere.
func (m Model) toggleIncludeArchived() (tea.Model, tea.Cmd) {
	m.includeArchived = !m.includeArchived
	label := "hidden"
	if m.includeArchived {
		label = "shown"
	}
	m.status = fmt.Sprintf("archived: %s", label)
	switch m.view {
	case viewProjects, viewSessions:
		return m, m.reloadSessions()
	case viewAll:
		m.allLoading = true
		return m, loadAllCmd(m.includeTmp, m.includeArchived)
	}
	return m, nil
}

// sessionRowForAction returns the session the current cursor sits on,
// across :sessions and :all. Returns ok=false in other views.
func (m Model) sessionRowForAction() (session.Row, bool) {
	switch m.view {
	case viewSessions:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessions) {
			return session.Row{}, false
		}
		return m.sessions[m.sessionCursor], true
	case viewAll:
		ar, ok := m.currentAllRow()
		if !ok {
			return session.Row{}, false
		}
		return ar.Row, true
	}
	return session.Row{}, false
}

// archiveAction archives the session under the cursor. Works in
// :sessions and :all. Refuses active or already-archived rows.
func (m Model) archiveAction() (tea.Model, tea.Cmd) {
	r, ok := m.sessionRowForAction()
	if !ok {
		return m, nil
	}
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

// restoreAction restores the archived session under the cursor. Works
// in :sessions and :all. Refuses non-archived rows.
func (m Model) restoreAction() (tea.Model, tea.Cmd) {
	r, ok := m.sessionRowForAction()
	if !ok {
		return m, nil
	}
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
	// :all is a static view; load once on first entry (or when the
	// user has explicitly reset via ^R). Mutations re-load via their
	// done-msg handlers.
	if v == viewAll && !m.allLoaded && !m.allLoading {
		m.allLoading = true
		return m, loadAllCmd(m.includeTmp, m.includeArchived)
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
	case viewAll:
		n := len(m.allVisible())
		if n == 0 {
			return m, nil
		}
		m.allCursor = clamp(m.allCursor+delta, 0, n-1)
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
	case viewAll:
		n := len(m.allVisible())
		if n == 0 {
			return m, nil
		}
		if target < 0 {
			target = n - 1
		}
		m.allCursor = clamp(target, 0, n-1)
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

// reloadAfterSessionMutation re-fires whichever list the current view
// cares about after an archive / restore / resume-return. :sessions +
// :projects reload the current project's sessions; :all reloads the
// cross-project list; others no-op.
func (m *Model) reloadAfterSessionMutation() tea.Cmd {
	switch m.view {
	case viewAll:
		m.allLoading = true
		return loadAllCmd(m.includeTmp, m.includeArchived)
	default:
		return m.reloadSessions()
	}
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
