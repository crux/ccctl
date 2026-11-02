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
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) header() string {
	title := fmt.Sprintf(" ccctl ui — :%s", m.view.label())
	hint := "  (1/2/3 view · tab/←→ switch · ↑↓ move · enter act · a archive · r restore · A show-arc · ^R refresh · +/- </> resize · : cmd · q quit)"
	line := headerStyle.Render(title) + footerStyle.Render(hint)
	// Cut the line at the terminal width: a wrapped header shifts the
	// whole frame down by a row.
	return padLine(lipgloss.NewStyle().MaxWidth(m.width).Render(line), m.width)
}

func (m Model) footer() string {
	if m.cmdMode {
		return cmdPromptStyle.Render(":") + m.cmdBuf + cmdPromptStyle.Render("_")
	}
	if m.status != "" {
		return statusStyle.Render(m.status)
	}
	return footerStyle.Render(fmt.Sprintf(" view=%s", m.view.label()))
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
	if m.view == viewProjects {
		projStyle = activePaneStyle
	} else if m.view == viewSessions {
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

// humanBytesCellTUI mirrors cmd/ccctl's humanBytesCell shape without
// pulling the CLI package in. Decimal-aligned, fixed small format.
func humanBytesCellTUI(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f G", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f M", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f K", float64(n)/float64(KB))
	}
	return fmt.Sprintf("%d B", n)
}
