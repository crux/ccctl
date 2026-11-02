package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12"))

	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	activePaneStyle = paneStyle.
			BorderForeground(lipgloss.Color("12"))

	paneTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252"))

	paneSubStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	cursorRowStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("238"))

	activeMarkerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("10"))

	sortColHeadStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("14"))

	peekBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Background(lipgloss.Color("0")).
			Padding(1, 2)

	peekTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("14"))

	peekTurnLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("244"))

	guessedMarkerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("11"))

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("208"))

	cmdPromptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10"))
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "initializing…"
	}

	header := m.header()
	footer := m.footer()
	bodyHeight := m.height - 2
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	var body string
	switch m.view {
	case viewProjects, viewSessions:
		body = m.renderBrowse(bodyHeight)
	case viewActive:
		body = m.renderActive(bodyHeight)
	case viewAll:
		body = m.renderAll(bodyHeight)
	}

	// Space quick-look sits ON TOP of the body — the row stays in the
	// list behind it so closing the overlay is a visually quiet beat.
	if m.peeking {
		body = m.overlayPeek(body, bodyHeight)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) header() string {
	title := fmt.Sprintf(" ccctl ui — :%s", m.view.label())
	hint := "  (1/2/3/4 view · tab/←→ switch · ↑↓ move · enter act · a archive · r restore · A show-arc · s/S sort · f status · / filter · ^R refresh · +/- </> resize · : cmd · q quit)"
	line := headerStyle.Render(title) + footerStyle.Render(hint)
	// Cut the line at the terminal width: a wrapped header shifts the
	// whole frame down by a row.
	return padLine(lipgloss.NewStyle().MaxWidth(m.width).Render(line), m.width)
}

func (m Model) footer() string {
	if m.cmdMode {
		return cmdPromptStyle.Render(":") + m.cmdBuf + cmdPromptStyle.Render("_")
	}
	if m.filterMode {
		return cmdPromptStyle.Render("/") + m.filterBuf + cmdPromptStyle.Render("_")
	}
	if m.status != "" {
		return statusStyle.Render(m.status)
	}
	if m.view == viewAll {
		return footerStyle.Render(fmt.Sprintf(" view=%s · sort=%s%s · status=%s · filter=%q",
			m.view.label(), m.allSort.label(), sortDirMark(m.allSortDesc), m.allStatusFilter.label(), m.filterBuf))
	}
	return footerStyle.Render(fmt.Sprintf(" view=%s", m.view.label()))
}

// overlayPeek centers the Space quick-look overlay over the body. The
// body underneath is passed through so the overlay has something to
// paint over (its background style gives it its own fill); nothing is
// mutated behind it, so closing is just a toggle.
func (m Model) overlayPeek(body string, h int) string {
	// Box sized to ~70% of the frame.
	boxW := m.width * 70 / 100
	if boxW < 48 {
		boxW = 48
	}
	if boxW > m.width-4 {
		boxW = m.width - 4
	}
	// Lipgloss places the box inside a bounded region; the body is used
	// as the backdrop so the caller doesn't need a separate dimming pass.
	box := peekBoxStyle.Width(boxW).Render(m.renderPeekBody(boxW - 6))
	return lipgloss.Place(
		m.width, h,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
	)
}

// renderPeekBody fills the overlay with the peek row's metadata and
// the first/last turn previews.
func (m Model) renderPeekBody(w int) string {
	r := m.peekRow
	home, _ := os.UserHomeDir()

	status := "idle"
	switch {
	case r.Archived:
		status = "archived"
	case m.activeSet.Confident[r.UUID]:
		status = "running"
		if st := m.activeSet.Status[r.UUID]; st != "" {
			status = "running · " + st
		}
	case m.activeSet.Guessed[r.UUID]:
		status = "guessed-active"
	}

	cwd := r.Meta.Cwd
	if cwd == "" {
		cwd = "—"
	} else if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + strings.TrimPrefix(cwd, home)
	}

	title := r.Meta.Display()

	var b strings.Builder
	b.WriteString(peekTitleStyle.Render("QUICK LOOK · " + title))
	b.WriteString("\n")
	b.WriteString(paneSubStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")
	rows := []kv{
		{"uuid", r.UUID},
		{"cwd", cwd},
		{"size", humanBytesCellTUI(r.Size)},
		{"mtime", r.MTime.Format("2006-01-02 15:04")},
		{"status", status},
	}
	b.WriteString(renderKV(rows, w))

	b.WriteString("\n")
	if !m.peekLoaded {
		b.WriteString(peekTurnLabelStyle.Render("first + last turn") + "  " + paneSubStyle.Render("(reading…)"))
		return b.String()
	}
	if r.Archived {
		b.WriteString(peekTurnLabelStyle.Render("first + last turn") + "  " + paneSubStyle.Render("(archived — restore first to peek inside)"))
		return b.String()
	}
	b.WriteString(peekTurnLabelStyle.Render("first turn"))
	b.WriteString("\n")
	b.WriteString(wrapTo(nonEmpty(m.peekTurns.First, "—"), w))
	b.WriteString("\n\n")
	b.WriteString(peekTurnLabelStyle.Render("last turn"))
	b.WriteString("\n")
	b.WriteString(wrapTo(nonEmpty(m.peekTurns.Last, "—"), w))
	b.WriteString("\n\n")
	b.WriteString(paneSubStyle.Render("space / esc close"))
	return b.String()
}

// wrapTo folds s onto lines of at most w columns, breaking on spaces
// where possible. Simple greedy wrap, no hyphenation.
func wrapTo(s string, w int) string {
	if w <= 1 {
		return s
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		cur := words[0]
		for _, word := range words[1:] {
			if lipgloss.Width(cur)+1+lipgloss.Width(word) > w {
				lines = append(lines, cur)
				cur = word
				continue
			}
			cur += " " + word
		}
		if cur != "" {
			lines = append(lines, cur)
		}
	}
	return strings.Join(lines, "\n")
}

// renderBrowse lays out the browse frame: PROJECTS and SESSIONS in a
// top row, DETAIL full-width below. Split ratios are driven by
// Model.topRowPct and Model.projColPct — resizable at runtime.
func (m Model) renderBrowse(h int) string {
	topH := h * m.topRowPct / 100
	if topH < 5 {
		topH = 5
	}
	bottomH := h - topH
	if bottomH < 4 {
		bottomH = 4
		topH = h - bottomH
	}

	// Border + padding costs: each pane spends 2 cols horizontally and
	// 2 rows vertically on border, plus 2 cols on padding. The content
	// region is (pane - 4) by (pane - 2).
	projW := m.width * m.projColPct / 100
	if projW < 22 {
		projW = 22
	}
	sessW := m.width - projW
	if sessW < 22 {
		sessW = 22
		projW = m.width - sessW
	}

	projStyle := paneStyle
	sessStyle := paneStyle
	switch m.view {
	case viewProjects:
		projStyle = activePaneStyle
	case viewSessions:
		sessStyle = activePaneStyle
	}

	topContentH := topH - 2
	if topContentH < 3 {
		topContentH = 3
	}
	bottomContentH := bottomH - 2
	if bottomContentH < 2 {
		bottomContentH = 2
	}

	// lipgloss Style.Width excludes the border, so each pane gets its
	// share minus the 2 border columns (as in renderActive).
	// lipgloss Height is a minimum: content taller than it grows the pane.
	// clipBlock keeps each pane at its height on small terminals.
	proj := projStyle.Width(projW - 2).Height(topContentH).Render(clipBlock(m.renderProjectsPane(projW-4, topContentH), topContentH))
	sess := sessStyle.Width(sessW - 2).Height(topContentH).Render(clipBlock(m.renderSessionsPane(sessW-4, topContentH), topContentH))
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, proj, sess)

	detail := paneStyle.Width(m.width - 2).Height(bottomContentH).Render(clipBlock(m.renderDetailPane(m.width-4, bottomContentH), bottomContentH))
	return lipgloss.JoinVertical(lipgloss.Left, topRow, detail)
}

// renderActive is the full-width :active view — one row per running
// claude process. The pane is single, cursor-navigable, and refreshes
// every activeRefreshInterval while this view is current.
func (m Model) renderActive(h int) string {
	contentH := h - 2
	if contentH < 3 {
		contentH = 3
	}
	w := m.width - 2
	innerW := w - 4

	count := len(m.active)
	title := m.paneTitle("ACTIVE", m.activeCursor, count, m.activeLoading && count == 0)
	refreshNote := ""
	if !m.activeLastRefresh.IsZero() {
		refreshNote = paneSubStyle.Render("  last " + m.activeLastRefresh.Format("15:04:05"))
	}
	header := title + refreshNote

	if count == 0 {
		msg := paneSubStyle.Render("(loading…)")
		if !m.activeLoading {
			msg = paneSubStyle.Render("(no running claude processes)")
		}
		body := header + "\n" + msg
		return activePaneStyle.Width(w).Height(contentH).Render(padBlock(body, contentH))
	}

	// Column widths. STATUS, PID and UUID stay fixed; TITLE and CWD
	// share the remainder.
	const (
		statusW = 8
		pidW    = 7
		uuidW   = 10
		gapW    = 1
	)
	flex := innerW - (statusW + pidW + uuidW + 3*gapW) - 2 // 2 cols reserve for title/cwd gap
	if flex < 10 {
		flex = 10
	}
	titleW := flex / 2
	cwdW := flex - titleW

	// Column headings line.
	headRow := paneSubStyle.Render(
		padRight("STATUS", statusW) + " " +
			padRight("PID", pidW) + " " +
			padRight("UUID", uuidW) + " " +
			padRight("TITLE", titleW) + " " +
			padRight("CWD", cwdW),
	)

	rowCap := contentH - 3 // header + heading + 1 line of breathing room
	if rowCap < 1 {
		rowCap = 1
	}

	home, _ := os.UserHomeDir()
	top := scrollTop(m.activeCursor, rowCap, count)
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(headRow)
	b.WriteString("\n")
	for i := top; i < top+rowCap && i < count; i++ {
		p := m.active[i]
		row := formatActiveRow(p, home, statusW, pidW, uuidW, titleW, cwdW)
		if i == m.activeCursor {
			row = cursorRowStyle.Render(padRight(row, innerW))
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return activePaneStyle.Width(w).Height(contentH).Render(padBlock(b.String(), contentH))
}

// formatActiveRow lays out one proc.Row into the fixed-plus-flex
// columns used by renderActive. A helper row of the daemon has no
// session: its note runs across the TITLE and CWD columns.
func formatActiveRow(p proc.Row, home string, statusW, pidW, uuidW, titleW, cwdW int) string {
	status := p.Label()
	if status == "" {
		status = "—"
	}
	if p.Role != proc.RoleSession {
		return padRight(status, statusW) + " " +
			padRight(fmt.Sprintf("%d", p.PID), pidW) + " " +
			padRight("", uuidW) + " " +
			padRight(truncate(p.Note, titleW+1+cwdW), titleW+1+cwdW)
	}
	uuid := p.UUID
	if len(uuid) > uuidW {
		uuid = uuid[:uuidW-1] + "…"
	}
	title := p.Title
	if title == "" {
		title = "(no title)"
	}
	cwd := p.Cwd
	if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + strings.TrimPrefix(cwd, home)
	}
	return padRight(status, statusW) + " " +
		padRight(fmt.Sprintf("%d", p.PID), pidW) + " " +
		padRight(uuid, uuidW) + " " +
		padRight(truncate(title, titleW), titleW) + " " +
		padRight(truncate(cwd, cwdW), cwdW)
}

// renderAll is the full-width :all cross-project view. Columns:
// [marker] MTIME · SIZE · PROJECT · UUID · TITLE. Sort / filter state
// is shown in the pane title and the footer; the row set is computed
// via m.allVisible() per render.
func (m Model) renderAll(h int) string {
	contentH := h - 2
	if contentH < 3 {
		contentH = 3
	}
	w := m.width - 2
	innerW := w - 4

	visible := m.allVisible()
	total := len(m.all)
	count := len(visible)

	title := m.paneTitle("ALL", m.allCursor, count, m.allLoading && count == 0)
	title = title + paneSubStyle.Render(fmt.Sprintf(" · %d total · sort=%s%s · status=%s",
		total, m.allSort.label(), sortDirMark(m.allSortDesc), m.allStatusFilter.label()))
	if m.filterBuf != "" {
		title = title + paneSubStyle.Render(fmt.Sprintf(" · /%s", m.filterBuf))
	}
	refreshNote := ""
	if !m.allLastRefresh.IsZero() {
		refreshNote = paneSubStyle.Render("  loaded " + m.allLastRefresh.Format("15:04:05"))
	}
	header := title + refreshNote

	if count == 0 {
		var msg string
		switch {
		case m.allLoading:
			msg = paneSubStyle.Render("(loading…)")
		case total == 0:
			msg = paneSubStyle.Render("(no sessions)")
		default:
			msg = paneSubStyle.Render("(no matches — adjust filter)")
		}
		body := header + "\n" + msg
		return activePaneStyle.Width(w).Height(contentH).Render(padBlock(body, contentH))
	}

	const (
		markerW = 2  // "* " / "~ " / "  "
		mtimeW  = 11 // "01-02 15:04"
		sizeW   = 7
		uuidW   = 10
		gapW    = 1
	)
	flex := innerW - (markerW + mtimeW + sizeW + uuidW + 4*gapW) - 2
	if flex < 20 {
		flex = 20
	}
	projW := flex * 40 / 100
	if projW < 14 {
		projW = 14
	}
	titleW := flex - projW
	if titleW < 10 {
		titleW = 10
	}

	headRow := padRight("", markerW) +
		colHead("MTIME", mtimeW, m.allSort == allSortMTime, m.allSortDesc) + " " +
		colHead("SIZE", sizeW, m.allSort == allSortSize, m.allSortDesc) + " " +
		colHead("PROJECT", projW, m.allSort == allSortProject, m.allSortDesc) + " " +
		colHead("UUID", uuidW, m.allSort == allSortUUID, m.allSortDesc) + " " +
		colHead("TITLE", titleW, m.allSort == allSortTitle, m.allSortDesc)

	rowCap := contentH - 3
	if rowCap < 1 {
		rowCap = 1
	}
	top := scrollTop(m.allCursor, rowCap, count)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(headRow)
	b.WriteString("\n")
	for i := top; i < top+rowCap && i < count; i++ {
		r := m.all[visible[i]]
		row := formatAllRow(r, m.sessionMarker(r.UUID), mtimeW, sizeW, projW, uuidW, titleW)
		if i == m.allCursor {
			row = cursorRowStyle.Render(padRight(row, innerW))
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return activePaneStyle.Width(w).Height(contentH).Render(padBlock(b.String(), contentH))
}

// colHead renders one column heading, highlighted (bold cyan) when
// that column is the current sort key — direction glyph appended inline
// so you can see at a glance which column + direction you're sorted by.
// Non-sort columns stay in the muted sub-style.
func colHead(label string, width int, active, desc bool) string {
	if active {
		text := label + sortDirMark(desc)
		return sortColHeadStyle.Render(padRight(text, width))
	}
	return paneSubStyle.Render(padRight(label, width))
}

// formatAllRow lays out one AllRow for the :all table. marker is a
// pre-rendered 2-column string (e.g. "* ", "~ ", "  ").
func formatAllRow(r AllRow, marker string, mtimeW, sizeW, projW, uuidW, titleW int) string {
	title := r.Meta.Display()
	if r.Archived {
		title = "[arc] " + title
	}
	uuid := r.UUID
	if len(uuid) > uuidW {
		uuid = uuid[:uuidW-1] + "…"
	}
	return marker +
		padRight(r.MTime.Format("01-02 15:04"), mtimeW) + " " +
		padRight(humanBytesCellTUI(r.Size), sizeW) + " " +
		padRight(truncate(r.ProjectName, projW), projW) + " " +
		padRight(uuid, uuidW) + " " +
		padRight(truncate(title, titleW), titleW)
}

// renderProjectsPane renders the PROJECTS list column in the given
// inner size (w = content width, h = pane content height after border).
func (m Model) renderProjectsPane(w, h int) string {
	count := len(m.projects)
	title := m.paneTitle("PROJECTS", m.projectCursor, count, m.projectsLoading && count == 0)
	rowCap := h - 2
	if rowCap < 1 {
		rowCap = 1
	}
	if count == 0 {
		msg := paneSubStyle.Render("(loading…)")
		if !m.projectsLoading {
			msg = paneSubStyle.Render("(no projects)")
		}
		return title + "\n" + msg + strings.Repeat("\n", max(0, rowCap-1))
	}

	home, _ := os.UserHomeDir()
	top := scrollTop(m.projectCursor, rowCap, count)
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	for i := top; i < top+rowCap && i < count; i++ {
		p := m.projects[i]
		line := formatProjectRow(p, home, w)
		if i == m.projectCursor {
			line = cursorRowStyle.Render(padRight(line, w))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) renderSessionsPane(w, h int) string {
	count := len(m.sessions)
	arcCount := 0
	for _, r := range m.sessions {
		if r.Archived {
			arcCount++
		}
	}
	title := m.paneTitle("SESSIONS", m.sessionCursor, count, m.sessionsLoading)
	if m.includeArchived && arcCount > 0 {
		title = title + paneSubStyle.Render(fmt.Sprintf(" · %d arc", arcCount))
	}
	rowCap := h - 2
	if rowCap < 1 {
		rowCap = 1
	}
	if count == 0 {
		var msg string
		switch {
		case m.sessionsLoading:
			msg = paneSubStyle.Render("(loading…)")
		case len(m.projects) == 0:
			msg = paneSubStyle.Render("")
		default:
			msg = paneSubStyle.Render("(no sessions)")
		}
		return title + "\n" + msg + strings.Repeat("\n", max(0, rowCap-1))
	}

	top := scrollTop(m.sessionCursor, rowCap, count)
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n")
	for i := top; i < top+rowCap && i < count; i++ {
		r := m.sessions[i]
		marker := m.sessionMarker(r.UUID)
		line := marker + formatSessionRow(r, w-lipgloss.Width(marker))
		if i == m.sessionCursor {
			line = cursorRowStyle.Render(padRight(line, w))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// renderDetailPane shows metadata of the row under the focused column.
func (m Model) renderDetailPane(w, h int) string {
	title := paneTitleStyle.Render("DETAIL")
	switch m.view {
	case viewProjects:
		if m.projectCursor < 0 || m.projectCursor >= len(m.projects) {
			return title + "\n" + paneSubStyle.Render("—")
		}
		return title + "\n" + m.projectDetail(m.projects[m.projectCursor], w)
	case viewSessions:
		if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessions) {
			return title + "\n" + paneSubStyle.Render("—")
		}
		return title + "\n" + m.sessionDetail(m.sessions[m.sessionCursor], w)
	}
	return title
}

func (m Model) projectDetail(p project.Info, w int) string {
	home, _ := os.UserHomeDir()
	cwd := p.Cwd
	if cwd == "" {
		cwd = "(unresolved — using encoded name)"
	} else if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + strings.TrimPrefix(cwd, home)
	}
	rows := []kv{
		{"path", cwd},
		{"encoded", p.EncodedName},
		{"sessions", fmt.Sprintf("%d", p.Sessions)},
		{"archived", fmt.Sprintf("%d", p.Archived)},
		{"size", humanBytesCellTUI(p.TotalSize)},
		{"latest", p.Latest.Format("2006-01-02 15:04")},
	}
	return renderKV(rows, w)
}

func (m Model) sessionDetail(r session.Row, w int) string {
	status := ""
	if m.activeLoaded {
		switch {
		case m.activeSet.Confident[r.UUID]:
			status = "active"
			if st := m.activeSet.Status[r.UUID]; st != "" {
				status = "active · " + st
			}
		case m.activeSet.Guessed[r.UUID]:
			status = "guessed-active"
		default:
			status = "idle"
		}
	}
	archived := "no"
	if r.Archived {
		archived = "yes"
	}
	rows := []kv{
		{"uuid", r.UUID},
		{"title", nonEmpty(r.Meta.Title(), "—")},
		{"summary", nonEmpty(r.Meta.Summary, "—")},
		{"cwd", nonEmpty(r.Meta.Cwd, "—")},
		{"size", humanBytesCellTUI(r.Size)},
		{"mtime", r.MTime.Format("2006-01-02 15:04")},
		{"archived", archived},
		{"status", nonEmpty(status, "—")},
	}
	return renderKV(rows, w)
}

type kv struct{ k, v string }

func renderKV(rows []kv, w int) string {
	keyW := 8
	var b strings.Builder
	for _, r := range rows {
		key := paneSubStyle.Render(padRight(r.k, keyW))
		val := truncate(r.v, w-keyW-1)
		b.WriteString(key)
		b.WriteString(" ")
		b.WriteString(val)
		b.WriteString("\n")
	}
	// No newline after the last row: it would add an empty line, and a
	// pane whose content is taller than its height grows and pushes the
	// header off the screen.
	return strings.TrimSuffix(b.String(), "\n")
}

// paneTitle formats the top line of a list pane: "<name> <cursor>/<count>".
func (m Model) paneTitle(name string, cursor, count int, loading bool) string {
	suffix := ""
	if loading {
		suffix = " …"
	}
	if count == 0 {
		return paneTitleStyle.Render(fmt.Sprintf("%s 0%s", name, suffix))
	}
	return paneTitleStyle.Render(fmt.Sprintf("%s %d/%d%s", name, cursor+1, count, suffix))
}

func formatProjectRow(p project.Info, home string, w int) string {
	date := p.Latest.Format("01-02 15:04")
	path := p.Cwd
	if path == "" {
		path = p.EncodedName
	} else if home != "" && strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}
	// Abbreviate: keep only the last two path segments when it would
	// otherwise not fit. Simple heuristic — avoids an unreadable left-
	// anchored truncation that would hide the project name.
	avail := w - len(date) - 1
	if avail < 1 {
		avail = 1
	}
	path = abbreviatePath(path, avail)
	return fmt.Sprintf("%s %s", date, path)
}

func formatSessionRow(r session.Row, w int) string {
	date := r.MTime.Format("01-02 15:04")
	title := r.Meta.Display()
	if r.Archived {
		title = "[arc] " + title
	}
	avail := w - len(date) - 1
	if avail < 1 {
		avail = 1
	}
	return fmt.Sprintf("%s %s", date, truncate(title, avail))
}

func (m Model) sessionMarker(uuid string) string {
	if !m.activeLoaded {
		return "  "
	}
	switch {
	case m.activeSet.Confident[uuid]:
		return activeMarkerStyle.Render("* ")
	case m.activeSet.Guessed[uuid]:
		return guessedMarkerStyle.Render("~ ")
	}
	return "  "
}

// abbreviatePath shortens path to fit limit chars; prefers to drop
// middle segments and keep the first and last. Shows ".../last/two"
// when nothing else fits.
func abbreviatePath(p string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(p) <= limit {
		return p
	}
	parts := strings.Split(p, string(filepath.Separator))
	if len(parts) >= 2 {
		short := ".../" + strings.Join(parts[len(parts)-2:], "/")
		if len(short) <= limit {
			return short
		}
	}
	// Last-resort left truncate.
	if limit >= 4 {
		return "..." + p[len(p)-(limit-3):]
	}
	return p[:limit]
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	// Lipgloss width-safe truncation on runes.
	runes := []rune(s)
	for i := len(runes); i > 0; i-- {
		cand := string(runes[:i]) + "…"
		if lipgloss.Width(cand) <= w {
			return cand
		}
	}
	return "…"
}

func padRight(s string, w int) string {
	plain := lipgloss.Width(s)
	if plain >= w {
		return s
	}
	return s + strings.Repeat(" ", w-plain)
}

func padLine(s string, w int) string {
	plain := lipgloss.Width(s)
	if plain >= w {
		return s
	}
	return s + strings.Repeat(" ", w-plain)
}

// clipBlock keeps the first h lines of s.
func clipBlock(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= h {
		return s
	}
	return strings.Join(lines[:h], "\n")
}

func padBlock(s string, h int) string {
	lines := strings.Count(s, "\n") + 1
	for i := lines; i < h; i++ {
		s += "\n"
	}
	return s
}

// scrollTop returns the top row index for a viewport of capacity cap
// that must keep cursor visible, over a list of length n.
func scrollTop(cursor, cap, n int) int {
	if n <= cap {
		return 0
	}
	if cursor < cap {
		return 0
	}
	top := cursor - cap + 1
	if top+cap > n {
		top = n - cap
	}
	if top < 0 {
		top = 0
	}
	return top
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// humanBytesCellTUI mirrors cmd/ccctl's humanBytesCell: a fixed-width
// cell with the decimal point in a stable column, so the "." stacks
// down the SIZE column. Width is 7 ("1234   X").
func humanBytesCellTUI(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%3d   B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	suffix := string("KMGTPE"[exp])
	if v >= 10 {
		return fmt.Sprintf("%3.0f   %s", v, suffix)
	}
	return fmt.Sprintf("%5.1f %s", v, suffix)
}
