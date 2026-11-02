package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/crux/ccctl/internal/proc"
	"github.com/crux/ccctl/internal/project"
	"github.com/crux/ccctl/internal/session"
	"github.com/spf13/cobra"
)

var statusHere bool

var statusCmd = &cobra.Command{
	Use:   "status [dir]",
	Short: "High-level overview of local Claude Code state",
	Long: `Print a summary of Claude Code session storage: project count,
session count (live + archived), total size, date range, oldest and
newest session, running processes, and the top 5 projects by session
count.

Scopes to a single project with --here, or a positional directory;
otherwise summarises all projects under ~/.claude/projects/.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusHere, "here", false, "restrict to the current project")
}

var statusUUIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type projectStats struct {
	EncodedDir       string
	DisplayPath      string // real cwd (from newest jsonl) or encoded basename
	LiveSessions     int
	ArchivedSessions int
	TotalBytes       int64
	// Oldest / newest tracked across .jsonl ONLY — archived .zip files
	// don't carry a readable title in the hot path.
	OldestMTime time.Time
	NewestMTime time.Time
	OldestFile  string
	NewestFile  string
}

func runStatus(cmd *cobra.Command, args []string) error {
	scoped := statusHere || len(args) > 0

	var projectDirs []string
	var scopedDisplay string

	if scoped {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		if statusHere && len(args) > 0 {
			return fmt.Errorf("--here and a positional <dir> are mutually exclusive")
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return err
		}
		encoded := project.Dir(real)
		if _, err := os.Stat(encoded); err != nil {
			fmt.Printf("No Claude sessions found for: %s\n", real)
			return nil
		}
		projectDirs = []string{encoded}
		scopedDisplay = real
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		root := filepath.Join(home, ".claude", "projects")
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				projectDirs = append(projectDirs, filepath.Join(root, e.Name()))
			}
		}
	}

	allStats := make([]projectStats, 0, len(projectDirs))
	for _, p := range projectDirs {
		s := collectProjectStats(p)
		if s.LiveSessions+s.ArchivedSessions > 0 {
			allStats = append(allStats, s)
		}
	}

	if len(allStats) == 0 {
		if scoped {
			fmt.Printf("No sessions in: %s\n", scopedDisplay)
		} else {
			fmt.Println("No sessions found.")
		}
		return nil
	}

	// Aggregate
	var totalLive, totalArch int
	var totalBytes int64
	var oldest, newest time.Time
	var oldestFile, newestFile string
	activeIn7d := 0
	weekAgo := time.Now().Add(-7 * 24 * time.Hour)
	for _, s := range allStats {
		totalLive += s.LiveSessions
		totalArch += s.ArchivedSessions
		totalBytes += s.TotalBytes
		if !s.OldestMTime.IsZero() && (oldest.IsZero() || s.OldestMTime.Before(oldest)) {
			oldest = s.OldestMTime
			oldestFile = s.OldestFile
		}
		if s.NewestMTime.After(newest) {
			newest = s.NewestMTime
			newestFile = s.NewestFile
		}
		if s.NewestMTime.After(weekAgo) {
			activeIn7d++
		}
	}
	procs, _ := proc.ListSessions()

	// Output
	if scoped {
		fmt.Printf("Claude Code status for: %s\n", scopedDisplay)
	} else {
		fmt.Println("Claude Code status")
	}
	if !scoped {
		fmt.Printf("  Projects:          %d (%d active in last 7 days)\n", len(allStats), activeIn7d)
	}
	if totalArch == 0 {
		fmt.Printf("  Sessions:          %d\n", totalLive)
	} else {
		fmt.Printf("  Sessions:          %d live, %d archived\n", totalLive, totalArch)
	}
	fmt.Printf("  Total size:        %s\n", humanBytes(totalBytes))

	if !oldest.IsZero() {
		spanDays := int(newest.Sub(oldest).Hours() / 24)
		fmt.Printf("  Date range:        %s → %s  (%d days)\n",
			oldest.Format("2006-01-02"), newest.Format("2006-01-02"), spanDays)
		fmt.Printf("  Oldest session:    %s  %s\n",
			oldest.Format("2006-01-02"), titleForFile(oldestFile))
		fmt.Printf("  Newest session:    %s  %s\n",
			newest.Format("2006-01-02 15:04"), titleForFile(newestFile))
	}

	if scoped {
		myRunning := 0
		for _, pr := range procs {
			if pr.Cwd == scopedDisplay {
				myRunning++
			}
		}
		fmt.Printf("  Running processes: %d (in this project)\n", myRunning)
	} else {
		fmt.Printf("  Running processes: %d\n", len(procs))
	}

	if !scoped && len(allStats) > 1 {
		sort.Slice(allStats, func(i, j int) bool {
			return allStats[i].LiveSessions+allStats[i].ArchivedSessions >
				allStats[j].LiveSessions+allStats[j].ArchivedSessions
		})
		fmt.Println()
		fmt.Println("  Top 5 projects by session count:")
		n := 5
		if n > len(allStats) {
			n = len(allStats)
		}
		for i := 0; i < n; i++ {
			s := allStats[i]
			total := s.LiveSessions + s.ArchivedSessions
			fmt.Printf("    %4d  %s\n", total, shortProject(s.DisplayPath))
		}
	}

	return nil
}

func collectProjectStats(encodedDir string) projectStats {
	stats := projectStats{EncodedDir: encodedDir}
	entries, err := os.ReadDir(encodedDir)
	if err != nil {
		return stats
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var uuid string
		var isLive, isArch bool
		switch {
		case strings.HasSuffix(name, ".jsonl"):
			uuid = strings.TrimSuffix(name, ".jsonl")
			isLive = true
		case strings.HasSuffix(name, ".zip"):
			uuid = strings.TrimSuffix(name, ".zip")
			isArch = true
		default:
			continue
		}
		if !statusUUIDRE.MatchString(uuid) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		stats.TotalBytes += info.Size()
		if isLive {
			stats.LiveSessions++
			full := filepath.Join(encodedDir, name)
			mtime := info.ModTime()
			if stats.OldestMTime.IsZero() || mtime.Before(stats.OldestMTime) {
				stats.OldestMTime = mtime
				stats.OldestFile = full
			}
			if mtime.After(stats.NewestMTime) {
				stats.NewestMTime = mtime
				stats.NewestFile = full
			}
		}
		if isArch {
			stats.ArchivedSessions++
		}
	}
	stats.DisplayPath = resolveProjectCwdFromFile(encodedDir, stats.NewestFile)
	return stats
}

// resolveProjectCwdFromFile pulls the cwd field from the newest .jsonl.
// Observed data: Claude Code can take 1–2 MB of session content before
// writing a `"cwd":`-bearing event, so we scan a 4 MB head of the file
// with bytes.Index for the marker and parse just the surrounding line.
// Returns the encoded dir basename as last resort — ugly, but unambiguous.
func resolveProjectCwdFromFile(encodedDir, newestFile string) string {
	if newestFile == "" {
		return filepath.Base(encodedDir)
	}
	f, err := os.Open(newestFile)
	if err != nil {
		return filepath.Base(encodedDir)
	}
	defer f.Close()
	const headChunk = 4 * 1024 * 1024
	buf := make([]byte, headChunk)
	n, _ := f.Read(buf)
	buf = buf[:n]

	cwdMarker := []byte(`"cwd":`)
	idx := bytes.Index(buf, cwdMarker)
	if idx < 0 {
		return filepath.Base(encodedDir)
	}
	// Back up to the start of the containing line and forward to its end.
	lineStart := bytes.LastIndexByte(buf[:idx], '\n') + 1
	lineEnd := idx + bytes.IndexByte(buf[idx:], '\n')
	if lineEnd < idx {
		lineEnd = len(buf)
	}
	var entry struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(buf[lineStart:lineEnd], &entry); err == nil && entry.Cwd != "" {
		return entry.Cwd
	}
	return filepath.Base(encodedDir)
}

// titleForFile picks a display title for a .jsonl: PIDSession.Name if
// the session is live, else session.ReadMeta.
func titleForFile(path string) string {
	if path == "" {
		return "(no title)"
	}
	uuid := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if ps, _ := proc.FindPIDBySessionID(uuid); ps != nil && ps.Title() != "" {
		return ps.Title()
	}
	if meta, err := session.ReadMeta(path); err == nil && meta.Title() != "" {
		return meta.Title()
	}
	return "(no title)"
}

// humanBytes renders a byte count with a single-letter unit suffix
// (K/M/G/…) separated by a space, and one decimal place for values
// below 10 of that unit. Base-1024.
//
//	999 bytes -> "999 B"
//	4700 KB   -> "4.7 M"
//	15 MB     -> "15 M"
//	199 MB    -> "199 M"
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	suffix := string("KMGTPE"[exp])
	if v >= 10 {
		return fmt.Sprintf("%.0f %s", v, suffix)
	}
	return fmt.Sprintf("%.1f %s", v, suffix)
}

// humanBytesCell is a fixed-7-char-wide, decimal-aligned variant of
// humanBytes for use in table columns. Each value takes the shape
// "III DU" where I is an integer digit (right-padded to width 3),
// D is the decimal digit (blanked when the value is an integer, so
// the "." column stays aligned), and U is the unit letter:
//
//	"999   B"    "4.7 M"  (within "  4.7 M")
//	"999   M"    " 15   M"
//
// The decimal point always sits at column 3, the unit at column 6.
func humanBytesCell(n int64) string {
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
	// %5.1f pads a value like 4.7 to "  4.7" so the "." lands at col 3
	// and the unit stays at col 6, aligned with the integer branch.
	return fmt.Sprintf("%5.1f %s", v, suffix)
}
