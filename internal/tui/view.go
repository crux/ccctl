package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	// Reserve 1 line for header, 1 for footer.
	bodyHeight := m.height - 2
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	var body string
	switch m.view {
	case viewProjects, viewSessions:
		body = m.renderMiller(bodyHeight)
	case viewActive:
		body = m.renderActive(bodyHeight)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) header() string {
	title := fmt.Sprintf(" ccctl ui — :%s", m.view.label())
	hint := "  (1 projects · 2 sessions · 3 active · : command · q quit)"
	line := headerStyle.Render(title) + footerStyle.Render(hint)
	return padLine(line, m.width)
}

func (m Model) footer() string {
	if m.cmdMode {
		return cmdPromptStyle.Render(":") + m.cmdBuf + cmdPromptStyle.Render("_")
	}
	if m.status != "" {
		return statusStyle.Render(m.status)
	}
	return footerStyle.Render(fmt.Sprintf(" view=%s  (Step 1 scaffold — data wiring arrives in Step 2)", m.view.label()))
}

// renderMiller lays out the browse-view frame: two narrow list columns
// (projects, sessions) and one wide detail column. The focused list is
// decided by m.view (projects view → projects pane; sessions view →
// sessions pane); the detail pane always sits on the right.
func (m Model) renderMiller(h int) string {
	// Column widths: narrow / narrow / wide, roughly 22% / 28% / 50%.
	// Minimums keep layouts legible on narrow terminals.
	projW := clamp(m.width*22/100, 16, 40)
	sessW := clamp(m.width*28/100, 20, 48)
	// Remaining width minus borders/padding per column (2 + 2 + 2 = 6ish).
	detailW := m.width - projW - sessW - 6
	if detailW < 20 {
		detailW = 20
	}

	projStyle := paneStyle
	sessStyle := paneStyle
	if m.view == viewProjects {
		projStyle = activePaneStyle
	} else if m.view == viewSessions {
		sessStyle = activePaneStyle
	}

	// Account for border + padding when sizing the content region.
	contentH := h - 2
	if contentH < 1 {
		contentH = 1
	}

	proj := projStyle.Width(projW).Height(contentH).Render(placeholderBlock(
		"PROJECTS",
		"(step 2)",
		projW-4, contentH-1,
	))
	sess := sessStyle.Width(sessW).Height(contentH).Render(placeholderBlock(
		"SESSIONS",
		"(step 2)",
		sessW-4, contentH-1,
	))
	detail := paneStyle.Width(detailW).Height(contentH).Render(placeholderBlock(
		"DETAIL",
		"title · uuid · cwd · size · mtime · status",
		detailW-4, contentH-1,
	))

	return lipgloss.JoinHorizontal(lipgloss.Top, proj, sess, detail)
}

// renderActive is the full-width single pane that will host the live
// active-sessions list from Step 3.
func (m Model) renderActive(h int) string {
	w := m.width
	contentH := h - 2
	if contentH < 1 {
		contentH = 1
	}
	return activePaneStyle.Width(w - 2).Height(contentH).Render(placeholderBlock(
		"ACTIVE",
		"live refresh arrives in Step 3",
		w-6, contentH-1,
	))
}

// placeholderBlock renders a titled empty pane body sized to w×h.
func placeholderBlock(title, subtitle string, w, h int) string {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	head := lipgloss.NewStyle().Bold(true).Render(title)
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(subtitle)
	body := head + "\n" + sub
	// Fill remaining lines with spaces so the border draws cleanly.
	lines := strings.Count(body, "\n") + 1
	for i := lines; i < h; i++ {
		body += "\n"
	}
	return body
}

func padLine(s string, w int) string {
	plain := lipgloss.Width(s)
	if plain >= w {
		return s
	}
	return s + strings.Repeat(" ", w-plain)
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
