package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionTitleCached(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	write := func(title string) {
		line := `{"type":"ai-title","aiTitle":"` + title + `"}` + "\n"
		if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	write("first")
	if got := sessionTitle(path, now); got != "first" {
		t.Fatalf("title = %q, want first", got)
	}

	write("second")
	if got := sessionTitle(path, now.Add(titleTTL/2)); got != "first" {
		t.Errorf("within TTL: title = %q, want the cached first", got)
	}
	if got := sessionTitle(path, now.Add(titleTTL+time.Second)); got != "second" {
		t.Errorf("after TTL: title = %q, want second", got)
	}

	if got := sessionTitle(filepath.Join(t.TempDir(), "missing.jsonl"), now); got != "" {
		t.Errorf("missing file: title = %q, want empty", got)
	}
}
