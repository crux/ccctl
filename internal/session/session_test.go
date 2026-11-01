package session

import (
	"strings"
	"testing"
)

func TestTitleOrder(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		title string
		disp  string
	}{
		{"none", []string{`{"type":"user","cwd":"/a"}`}, "", "(no title)"},
		{"ai only", []string{
			`{"type":"ai-title","aiTitle":"old ai"}`,
			`{"type":"ai-title","aiTitle":"new ai"}`,
		}, "new ai", "new ai"},
		{"ai beats summary", []string{
			`{"type":"summary","summary":"sum"}`,
			`{"type":"ai-title","aiTitle":"ai"}`,
		}, "ai", "ai"},
		{"custom beats ai", []string{
			`{"type":"custom-title","customTitle":"mine"}`,
			`{"type":"ai-title","aiTitle":"ai"}`,
		}, "mine", "mine"},
		{"custom with summary", []string{
			`{"type":"custom-title","customTitle":"mine"}`,
			`{"type":"summary","summary":"sum"}`,
			`{"type":"ai-title","aiTitle":"ai"}`,
		}, "mine", "mine (sum)"},
	}
	for _, c := range cases {
		m, err := ReadMetaBytes([]byte(strings.Join(c.lines, "\n") + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if m.Title() != c.title || m.Display() != c.disp {
			t.Errorf("%s: Title() = %q, Display() = %q; want %q, %q", c.name, m.Title(), m.Display(), c.title, c.disp)
		}
	}
}
