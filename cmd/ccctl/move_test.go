package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifySource(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "0b6c2a4e-1f3d-4c8e-9a7b-5d2e1f0c3b4a"
	if err := os.Mkdir(filepath.Join(dir, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}

	if k, err := classifySource(id, false); err != nil || k != sourceSession {
		t.Errorf("session id: %v, %v", k, err)
	}
	if k, err := classifySource("proj", false); err != nil || k != sourceDir {
		t.Errorf("directory: %v, %v", k, err)
	}
	if _, err := classifySource("missing", false); err == nil {
		t.Error("missing path accepted")
	}
	if k, err := classifySource("missing", true); err != nil || k != sourceDir {
		t.Errorf("sessions-only: %v, %v", k, err)
	}

	if err := os.Mkdir(filepath.Join(dir, id), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := classifySource(id, false); err == nil || !strings.Contains(err.Error(), "./"+id) {
		t.Errorf("ambiguous id: %v", err)
	}
	if k, err := classifySource("./"+id, false); err != nil || k != sourceDir {
		t.Errorf("./id: %v, %v", k, err)
	}
}
