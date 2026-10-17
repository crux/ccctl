// Package session reads and summarizes Claude Code session .jsonl files.
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Meta is the minimal metadata we extract from a session .jsonl for
// display in listings. More fields can be added as higher-level commands
// need them.
type Meta struct {
	CustomTitle string
	Summary     string
}

// Display picks a display-ready label from available fields.
func (m Meta) Display() string {
	switch {
	case m.CustomTitle != "" && m.Summary != "":
		return m.CustomTitle + " (" + m.Summary + ")"
	case m.CustomTitle != "":
		return m.CustomTitle
	case m.Summary != "":
		return m.Summary
	default:
		return "(no title)"
	}
}

// Size of each reverse-scan chunk. Large enough that we almost always
// find both fields in the final chunk, small enough that we never read
// an unnecessary fraction of a big session.
const chunkSize int64 = 1024 * 1024

// Hard cap on how far from EOF we scan looking for still-missing fields.
// Protects against pathological cases such as sessions without a summary
// line, where otherwise we'd scan the full file in vain. A session that
// legitimately keeps display metadata further than this from the end is
// rare; the display gracefully omits whichever field was not found.
const maxScanBytes int64 = 4 * 1024 * 1024

// ReadMeta returns the "last occurrence wins" value of each tracked
// field in a Claude session .jsonl file on disk. Convenience wrapper
// around readMeta for the common file-path case.
func ReadMeta(path string) (Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Meta{}, err
	}
	return readMeta(f, fi.Size())
}

// ReadMetaBytes is the in-memory variant, used for .jsonl content
// extracted from an archive without touching disk.
func ReadMetaBytes(b []byte) (Meta, error) {
	return readMeta(bytes.NewReader(b), int64(len(b)))
}

// readMeta reads session metadata by walking the backing bytes in
// reverse, 1 MB at a time, stopping as soon as every tracked field has
// been resolved OR maxScanBytes has been scanned without resolving them.
// For typical sessions this reads a few MB regardless of total size.
func readMeta(r io.ReaderAt, size int64) (Meta, error) {
	var m Meta
	if size == 0 {
		return m, nil
	}

	titleFound := false
	summaryFound := false

	// `carry` holds the bytes of a partial line taken from the START of
	// a newer window — its completion sits at the END of this (older)
	// window and we join them before processing.
	var carry []byte

	windowEnd := size
	for windowEnd > 0 {
		if titleFound && summaryFound {
			break
		}
		if size-windowEnd >= maxScanBytes {
			break
		}

		windowStart := windowEnd - chunkSize
		if windowStart < 0 {
			windowStart = 0
		}

		buf := make([]byte, windowEnd-windowStart)
		if _, err := r.ReadAt(buf, windowStart); err != nil && !errors.Is(err, io.EOF) {
			return m, err
		}

		// Join carry from the previous (newer) iteration — it is the
		// suffix of a line whose prefix lives at the end of THIS buf.
		if len(carry) > 0 {
			buf = append(buf, carry...)
			carry = nil
		}

		// When windowStart > 0, the FIRST line in buf is partial (its
		// prefix lives in the previous window, which we'll read next).
		// Set that aside as new carry; process only what comes after
		// the first newline.
		processStart := 0
		if windowStart > 0 {
			nl := bytes.IndexByte(buf, '\n')
			if nl < 0 {
				// No newline in this (plus carry) at all — a single line
				// spans at least one full chunk. Carry everything back.
				carry = buf
				windowEnd = windowStart
				continue
			}
			carry = append([]byte(nil), buf[:nl]...)
			processStart = nl + 1
		}

		// Scan complete lines in buf[processStart:] in file (forward)
		// order so the latest occurrence in this window wins locally.
		var winTitle, winSummary string
		for _, line := range bytes.Split(buf[processStart:], []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var entry struct {
				Type        string `json:"type"`
				CustomTitle string `json:"customTitle"`
				Summary     string `json:"summary"`
			}
			if err := json.Unmarshal(line, &entry); err != nil {
				continue // skip malformed lines rather than aborting
			}
			switch entry.Type {
			case "custom-title":
				if entry.CustomTitle != "" {
					winTitle = entry.CustomTitle
				}
			case "summary":
				if entry.Summary != "" {
					winSummary = entry.Summary
				}
			}
		}

		// Promote window-local values to the result only if we have not
		// already set that field from a newer window.
		if !titleFound && winTitle != "" {
			m.CustomTitle = winTitle
			titleFound = true
		}
		if !summaryFound && winSummary != "" {
			m.Summary = winSummary
			summaryFound = true
		}

		windowEnd = windowStart
	}

	return m, nil
}
