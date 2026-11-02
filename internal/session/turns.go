package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// Turns holds a short preview of a session's content: the first and the
// last user turn, each truncated to the character budget asked for.
// Either field may be empty when the session carries no readable user
// turn.
type Turns struct {
	First string
	Last  string
}

// ReadTurns opens path and returns a Turns preview. The forward scan
// stops at the first user turn; the reverse scan reads a bounded tail
// looking for the last one. Each returned string is truncated to max
// characters (… suffix when truncated).
func ReadTurns(path string, max int) (Turns, error) {
	f, err := os.Open(path)
	if err != nil {
		return Turns{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Turns{}, err
	}
	return readTurns(f, fi.Size(), max)
}

// ReadTurnsBytes is the in-memory variant for archived sessions.
func ReadTurnsBytes(b []byte, max int) (Turns, error) {
	return readTurns(bytes.NewReader(b), int64(len(b)), max)
}

// readTurns extracts first + last user turn text from a .jsonl backing.
// First turn comes from a forward scan over the head. Last comes from
// the same reverse-chunk pattern readMeta uses, stopping as soon as a
// user turn is found or maxScanBytes have been scanned.
func readTurns(r io.ReaderAt, size int64, max int) (Turns, error) {
	var t Turns
	if size == 0 {
		return t, nil
	}

	t.First = firstUserTurn(r, size)

	last, err := lastUserTurn(r, size)
	if err != nil {
		return t, err
	}
	t.Last = last

	if max > 0 {
		t.First = truncateRunes(t.First, max)
		t.Last = truncateRunes(t.Last, max)
	}
	return t, nil
}

// firstUserTurn scans up to the first few MB of the file looking for a
// user-turn event, returning its message text.
func firstUserTurn(r io.ReaderAt, size int64) string {
	// A session's user turn usually sits in the first MB, often the
	// first few lines. The forward scan is bounded by maxScanBytes to
	// avoid pathological reads on sessions whose first user turn sits
	// far from the start.
	head := size
	if head > maxScanBytes {
		head = maxScanBytes
	}
	buf := make([]byte, head)
	if _, err := r.ReadAt(buf, 0); err != nil && err != io.EOF {
		return ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		text := extractUserText(scanner.Bytes())
		if text != "" {
			return text
		}
	}
	return ""
}

// lastUserTurn reverse-scans the file in chunks, newest window first,
// and returns the first user turn it finds (which is the last one in
// file order). Bounded by maxScanBytes.
func lastUserTurn(r io.ReaderAt, size int64) (string, error) {
	var carry []byte
	windowEnd := size
	for windowEnd > 0 {
		if size-windowEnd >= maxScanBytes {
			break
		}
		windowStart := windowEnd - chunkSize
		if windowStart < 0 {
			windowStart = 0
		}
		buf := make([]byte, windowEnd-windowStart)
		if _, err := r.ReadAt(buf, windowStart); err != nil && err != io.EOF {
			return "", err
		}
		if len(carry) > 0 {
			buf = append(buf, carry...)
			carry = nil
		}

		processStart := 0
		if windowStart > 0 {
			nl := bytes.IndexByte(buf, '\n')
			if nl < 0 {
				carry = buf
				windowEnd = windowStart
				continue
			}
			carry = append([]byte(nil), buf[:nl]...)
			processStart = nl + 1
		}

		// Walk lines in reverse order (so the last user turn in this
		// window wins — matches readMeta's "last-wins" semantics but
		// here we want THE last, not a merged last-wins across windows).
		lines := bytes.Split(buf[processStart:], []byte{'\n'})
		for i := len(lines) - 1; i >= 0; i-- {
			text := extractUserText(lines[i])
			if text != "" {
				return text, nil
			}
		}
		windowEnd = windowStart
	}
	return "", nil
}

// extractUserText parses a .jsonl line and returns the user-turn text,
// or "" when the line isn't a user turn or carries no readable content.
// Handles both string content and the array-of-blocks shape.
func extractUserText(line []byte) string {
	if len(line) == 0 {
		return ""
	}
	var entry struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		return ""
	}
	if entry.Type != "user" {
		return ""
	}

	// Message.content is either a string or an array of content blocks
	// of the form {type: "text", text: "…"}. Try the string form first,
	// then the array form.
	var msgStr struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(entry.Message, &msgStr); err == nil && msgStr.Content != "" {
		return strings.TrimSpace(msgStr.Content)
	}
	var msgArr struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(entry.Message, &msgArr); err == nil {
		var parts []string
		for _, c := range msgArr.Content {
			if c.Type == "text" && c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) > 0 {
			return strings.TrimSpace(strings.Join(parts, "\n"))
		}
	}
	return ""
}

// truncateRunes shortens s to max runes, appending "…" when truncated.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
