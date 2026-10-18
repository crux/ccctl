package main

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
	"text/tabwriter"
	"time"

	"github.com/crux/claudectl/internal/proc"
	"github.com/crux/claudectl/internal/project"
	"github.com/crux/claudectl/internal/session"
	"github.com/spf13/cobra"
)

var (
	sessionsArchived bool
	sessionsWorkers  = 16 // bound concurrent meta reads to avoid piling onto the file system
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions [dir]",
	Short: "List stored sessions for a project directory, newest first",
	Long: `List sessions stored for a project directory under
~/.claude/projects/<encoded-project>/. Default is the current working
directory. Live sessions only by default; -a / --archived also lists
sessions previously packed via claude-archive-session (marked
"[archived]").`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSessions,
}

func init() {
	sessionsCmd.Flags().BoolVarP(&sessionsArchived, "archived", "a", false, "include archived (.zip) sessions")
}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type sessionRow struct {
	MTime    time.Time
	Size     int64
	UUID     string
	Meta     session.Meta
	Archived bool
}

func runSessions(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	realDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return err
	}

	projectDir := project.Dir(realDir)
	if _, err := os.Stat(projectDir); err != nil {
		fmt.Printf("No Claude sessions found for: %s\n", realDir)
		return nil
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return fmt.Errorf("read project dir: %w", err)
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
		case sessionsArchived && strings.HasSuffix(name, ".zip"):
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
		fmt.Printf("No Claude sessions found for: %s\n", realDir)
		return nil
	}

	// Fetch active-session UUIDs in parallel with the meta reads — the
	// ps/lsof calls underneath take roughly as long as all the file I/O,
	// so overlapping them keeps total latency close to max(both).
	activeChan := make(chan proc.ActiveSet, 1)
	go func() {
		set, _ := proc.ActiveUUIDs()
		activeChan <- set
	}()

	// Fan out meta reads with bounded concurrency.
	rows := make([]sessionRow, len(pendings))
	sem := make(chan struct{}, sessionsWorkers)
	var wg sync.WaitGroup
	for i, p := range pendings {
		wg.Add(1)
		go func(i int, p pending) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var meta session.Meta
			if p.archived {
				meta, _ = readMetaFromArchive(p.path, p.uuid)
			} else {
				meta, _ = session.ReadMeta(p.path)
			}
			rows[i] = sessionRow{MTime: p.mtime, Size: p.size, UUID: p.uuid, Meta: meta, Archived: p.archived}
		}(i, p)
	}
	wg.Wait()
	active := <-activeChan

	sort.Slice(rows, func(i, j int) bool { return rows[i].MTime.After(rows[j].MTime) })

	fmt.Printf("Claude sessions for: %s\n\n", realDir)
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	// Leading 1-char gutter: '*' confident-active, '~' guessed-active,
	// ' ' otherwise. Fixed left-margin position makes it scannable.
	// SIZE is the on-disk .jsonl (or .zip) size — a cheap, free-from-
	// directory-walk proxy for session weight. When the FTS index lands
	// it can be replaced or complemented with precomputed message counts.
	fmt.Fprintln(w, " \tMTIME\tSIZE\tUUID\tTITLE")
	for _, r := range rows {
		marker := " "
		switch {
		case active.Confident[r.UUID]:
			marker = "*"
		case active.Guessed[r.UUID]:
			marker = "~"
		}
		suffix := ""
		switch {
		case r.Archived:
			suffix = "  [archived]"
		case active.Confident[r.UUID]:
			// For live sessions, append (status) when non-idle. Idle
			// is the common state and left unmarked.
			if st := active.Status[r.UUID]; st != "" && st != "idle" {
				suffix = "  (" + st + ")"
			}
		}
		// %5s right-aligns short values (e.g. "15 M" → " 15 M") so the
		// numeric portion of the SIZE column stacks visually. Width 5
		// fits the maximum: "199 M", "4.7 M", "1.5 G", "999 B".
		fmt.Fprintf(w, "%s\t%s\t%5s\t%s\t%s%s\n",
			marker,
			r.MTime.Format("2006-01-02 15:04"),
			humanBytes(r.Size),
			r.UUID,
			r.Meta.Display(),
			suffix)
	}
	return w.Flush()
}

// readMetaFromArchive opens a zip archive, finds the <uuid>.jsonl entry
// inside it, reads it into memory, and extracts meta via the in-memory
// variant. Returns zero Meta on any error (callers already tolerate it).
func readMetaFromArchive(archivePath, uuid string) (session.Meta, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return session.Meta{}, err
	}
	defer zr.Close()
	target := uuid + ".jsonl"
	for _, f := range zr.File {
		if filepath.Base(f.Name) != target {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return session.Meta{}, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return session.Meta{}, err
		}
		return session.ReadMetaBytes(b)
	}
	return session.Meta{}, fmt.Errorf("%s not found in %s", target, archivePath)
}
