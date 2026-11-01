package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
)

// Every view must fit the terminal exactly. A pane that grows past its
// height pushes the header off the screen; one that is too wide breaks
// the right borders.
func TestViewFitsTerminal(t *testing.T) {
	sizes := [][2]int{{100, 20}, {150, 31}, {150, 34}, {200, 50}}
	for _, v := range []view{viewProjects, viewSessions, viewActive} {
		for _, sz := range sizes {
			m := New()
			m.width, m.height = sz[0], sz[1]
			m.view = v
			m.projects = []project.Info{{EncodedName: "-home-dev-src-webshop", Cwd: "/home/dev/src/webshop", Sessions: 2, Latest: time.Now()}}
			m.sessions = []session.Row{
				{UUID: "3f1c9a52-7d4e-4b1a-9c2e-5a8d1f0b6e71", MTime: time.Now(), Meta: session.Meta{AITitle: "Checkout: VAT rounding", Cwd: "/home/dev/src/webshop"}},
				{UUID: "0e9a6b3c-2d7f-4c1e-8b5a-7f3d9e2c1b64", MTime: time.Now(), Meta: session.Meta{AITitle: "Stripe webhook retries"}},
			}
			lines := strings.Split(m.View(), "\n")
			if len(lines) != m.height {
				t.Errorf("view %d at %dx%d: %d lines", v, m.width, m.height, len(lines))
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > m.width {
					t.Errorf("view %d at %dx%d: line %d is %d wide", v, m.width, m.height, i, w)
					break
				}
			}
		}
	}
}
