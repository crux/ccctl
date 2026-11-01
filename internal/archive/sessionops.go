package archive

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
)

// SessionArchiveResult reports what ArchiveSession produced on success.
type SessionArchiveResult struct {
	ArchivePath string // absolute path to <uuid>.zip
	SourceDir   string // the project directory the session lives in
}

// ArchiveSession packs <uuid>.jsonl (and companion <uuid>/ folder if
// present) into <uuid>.zip in the same project directory, then removes
// the originals. The archive is created, validated, and only then are
// the originals deleted — the zip is never left half-written.
//
// Refuses to archive an active session or overwrite an existing archive.
func ArchiveSession(uuid string) (SessionArchiveResult, error) {
	sourceFile, err := project.FindSessionFile(uuid)
	if err != nil {
		return SessionArchiveResult{}, err
	}
	if sourceFile == "" {
		return SessionArchiveResult{}, fmt.Errorf("session %s not found in ~/.claude/projects/", uuid)
	}

	sessionDir := filepath.Dir(sourceFile)
	archivePath := filepath.Join(sessionDir, uuid+".zip")

	if _, err := os.Stat(archivePath); err == nil {
		return SessionArchiveResult{}, fmt.Errorf("%s already exists; refusing to overwrite", archivePath)
	}
	if proc.IsFileOpen(sourceFile) {
		return SessionArchiveResult{}, fmt.Errorf("session %s is open by a running claude process; close it first", uuid)
	}

	items := []string{uuid + ".jsonl"}
	companion := filepath.Join(sessionDir, uuid)
	if info, err := os.Stat(companion); err == nil && info.IsDir() {
		items = append(items, uuid)
	}

	if err := Create(archivePath, sessionDir, items); err != nil {
		os.Remove(archivePath)
		return SessionArchiveResult{}, fmt.Errorf("zip creation failed: %w", err)
	}
	if err := Validate(archivePath); err != nil {
		os.Remove(archivePath)
		return SessionArchiveResult{}, fmt.Errorf("zip validation failed: %w", err)
	}
	for _, item := range items {
		if err := os.RemoveAll(filepath.Join(sessionDir, item)); err != nil {
			return SessionArchiveResult{ArchivePath: archivePath, SourceDir: sessionDir},
				fmt.Errorf("archived but original not removed: %s: %w", item, err)
		}
	}
	return SessionArchiveResult{ArchivePath: archivePath, SourceDir: sessionDir}, nil
}

// SessionRestoreResult reports what RestoreSession produced on success.
type SessionRestoreResult struct {
	JSONLPath   string // absolute path to the restored <uuid>.jsonl
	ArchivePath string // where the zip was before removal
	SourceDir   string // the project directory
}

// RestoreSession unpacks <uuid>.zip in place, then removes the archive.
// Refuses to overwrite an existing <uuid>.jsonl.
func RestoreSession(uuid string) (SessionRestoreResult, error) {
	archivePath, err := project.FindArchive(uuid)
	if err != nil {
		return SessionRestoreResult{}, err
	}
	if archivePath == "" {
		return SessionRestoreResult{}, fmt.Errorf("archive %s.zip not found in ~/.claude/projects/", uuid)
	}

	sessionDir := filepath.Dir(archivePath)
	targetFile := filepath.Join(sessionDir, uuid+".jsonl")

	if _, err := os.Stat(targetFile); err == nil {
		return SessionRestoreResult{}, fmt.Errorf("%s already exists; refusing to overwrite", targetFile)
	}

	if err := Extract(archivePath, sessionDir); err != nil {
		return SessionRestoreResult{}, fmt.Errorf("extract failed: %w", err)
	}
	if err := os.Remove(archivePath); err != nil {
		return SessionRestoreResult{JSONLPath: targetFile, ArchivePath: archivePath, SourceDir: sessionDir},
			fmt.Errorf("restored but archive not removed: %w", err)
	}
	return SessionRestoreResult{JSONLPath: targetFile, ArchivePath: archivePath, SourceDir: sessionDir}, nil
}
