package session

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Row is one session as surfaced to listers (CLI and TUI).
type Row struct {
	UUID     string
	Path     string // full path to .jsonl or .zip
	MTime    time.Time
	Size     int64
	Meta     Meta
	Archived bool
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// listWorkers bounds concurrent meta reads to avoid piling onto the file
// system. Matches the inline worker count the CLI used previously.
const listWorkers = 16

// ListInProject enumerates the sessions stored under projectDir — one .jsonl
// per live session, optionally also one .zip per archived session when
// includeArchived is true — and returns rows sorted newest-first. Each row
// has Meta populated via the reverse-scan reader. ReadMeta failures are
// tolerated (the row keeps a zero Meta).
//
// Returns an empty slice when the directory has no session files; returns an
// error only when the directory itself cannot be read.
func ListInProject(projectDir string, includeArchived bool) ([]Row, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}

	type pending struct {
		path     string
		uuid     string
		mtime    time.Time
		size     int64
		archived bool
	}
	var pendings []pending
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var uuid string
		var archived bool
		switch {
		case strings.HasSuffix(name, ".jsonl"):
			uuid = strings.TrimSuffix(name, ".jsonl")
		case includeArchived && strings.HasSuffix(name, ".zip"):
			uuid = strings.TrimSuffix(name, ".zip")
			archived = true
		default:
			continue
		}
		if !uuidRE.MatchString(uuid) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		pendings = append(pendings, pending{
			path:     filepath.Join(projectDir, name),
			uuid:     uuid,
			mtime:    info.ModTime(),
			size:     info.Size(),
			archived: archived,
		})
	}
	if len(pendings) == 0 {
		return nil, nil
	}

	rows := make([]Row, len(pendings))
	sem := make(chan struct{}, listWorkers)
	var wg sync.WaitGroup
	for i, p := range pendings {
		wg.Add(1)
		go func(i int, p pending) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var meta Meta
			if p.archived {
				meta, _ = readMetaFromArchive(p.path, p.uuid)
			} else {
				meta, _ = ReadMeta(p.path)
			}
			rows[i] = Row{UUID: p.uuid, Path: p.path, MTime: p.mtime, Size: p.size, Meta: meta, Archived: p.archived}
		}(i, p)
	}
	wg.Wait()

	sort.Slice(rows, func(i, j int) bool { return rows[i].MTime.After(rows[j].MTime) })
	return rows, nil
}

// readMetaFromArchive opens a zip archive, finds the <uuid>.jsonl entry
// inside it, reads it into memory, and extracts meta via the in-memory
// variant. Returns zero Meta on any error (callers already tolerate it).
func readMetaFromArchive(archivePath, uuid string) (Meta, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return Meta{}, err
	}
	defer zr.Close()
	target := uuid + ".jsonl"
	for _, f := range zr.File {
		if filepath.Base(f.Name) != target {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return Meta{}, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return Meta{}, err
		}
		return ReadMetaBytes(b)
	}
	return Meta{}, fmt.Errorf("%s not found in %s", target, archivePath)
}
