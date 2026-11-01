package search

import (
	"bytes"
	"encoding/binary"
	"regexp"
	"sync"
	"unicode/utf8"
)

// matcher decides whether a byte slice contains the search pattern. It
// is the hot path of a search: every byte of every session file passes
// through contains. Exact match positions in decoded message text still
// come from re (FindAllStringIndex), but only for the few lines that
// contain the pattern.
//
// Speed: a case-insensitive Go regexp scans at roughly 35 MB/s, so a
// 230 MB session file alone took about 7 s. The literal paths below run
// at memory speed. An index (phase 2) uses the same scan for its first
// build; after that, session files only grow, so it reads only the
// appended tails (see the package comment).
type matcher struct {
	re     *regexp.Regexp // the pattern as a literal regexp; positions and the Unicode fallback
	needle []byte         // the pattern; lower-case ASCII when foldASCII
	// foldASCII: case-insensitive with an ASCII pattern. Lower-cases the
	// input in windows, 8 bytes at a time, and compares with bytes.Index.
	// (Unlike (?i), this ignores the two non-ASCII letters that Unicode
	// folds onto ASCII: U+212A Kelvin sign and U+017F long s.)
	//
	// Measured and rejected: scanning for the pattern's rarest byte in
	// both cases with IndexByte. Fast for rare letters (z, x), but common
	// ones (k in "makefile") stop the scan so often that it is slower.
	foldASCII bool
	fold      bool // case-insensitive
	// anchor: for a case-insensitive pattern with non-ASCII letters, its
	// longest ASCII part, lower-case ("sebrink" in "Lüsebrink"). Every
	// match contains it, so a fast check for the anchor runs first and
	// the slow Unicode regexp only where the anchor occurs.
	anchor []byte
}

func newMatcher(pattern string, caseSensitive bool) (*matcher, error) {
	flags := "(?i)"
	if caseSensitive {
		flags = ""
	}
	re, err := regexp.Compile(flags + regexp.QuoteMeta(pattern))
	if err != nil {
		return nil, err
	}
	m := &matcher{re: re, fold: !caseSensitive}
	switch {
	case caseSensitive:
		m.needle = []byte(pattern)
	case isASCII(pattern):
		m.foldASCII = true
		m.needle = lowerASCII(nil, []byte(pattern))
	default:
		if a := longestASCIIRun(pattern); len(a) >= 3 {
			m.anchor = lowerASCII(nil, []byte(a))
		}
	}
	return m, nil
}

// longestASCIIRun returns the longest substring of s without non-ASCII
// bytes.
func longestASCIIRun(s string) string {
	best, start := "", 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] >= utf8.RuneSelf {
			if i-start > len(best) {
				best = s[start:i]
			}
			start = i + 1
		}
	}
	return best
}

// contains reports whether b contains the pattern.
func (m *matcher) contains(b []byte) bool {
	switch {
	case !m.fold:
		return bytes.Contains(b, m.needle)
	case m.foldASCII:
		return containsFoldASCII(b, m.needle)
	case m.anchor != nil && !containsFoldASCII(b, m.anchor):
		return false
	default:
		return m.re.Match(b)
	}
}

// foldChunk is the size of the windows that containsFoldASCII lowers
// at a time. A variable so tests can make it tiny.
var foldChunk = 1 << 20

var foldBuf = sync.Pool{New: func() any { b := make([]byte, 0, foldChunk+64); return &b }}

// containsFoldASCII reports whether hay contains needle (lower-case
// ASCII), ignoring ASCII case. It lowers hay window by window into a
// pooled buffer; consecutive windows overlap by len(needle)-1 bytes, so
// a match across a window border is found.
func containsFoldASCII(hay, needle []byte) bool {
	n := len(needle)
	if n == 0 {
		return true
	}
	bp := foldBuf.Get().(*[]byte)
	defer foldBuf.Put(bp)
	for start := 0; start < len(hay); start += foldChunk {
		end := min(start+foldChunk+n-1, len(hay))
		*bp = lowerASCII((*bp)[:0], hay[start:end])
		if bytes.Index(*bp, needle) >= 0 {
			return true
		}
		if end == len(hay) {
			break
		}
	}
	return false
}

// lowerASCII writes src with A-Z lowered into dst (reusing its
// capacity) and returns it. Other bytes, including UTF-8 sequences,
// stay as they are. It works on 8 bytes at a time (SWAR): for each
// byte below 0x80 it computes "is A-Z" in the byte's high bit and sets
// bit 5 (0x20) where that holds.
func lowerASCII(dst, src []byte) []byte {
	if cap(dst) < len(src) {
		dst = make([]byte, len(src))
	} else {
		dst = dst[:len(src)]
	}
	const (
		ones = 0x0101010101010101
		high = 0x8080808080808080
	)
	i := 0
	for ; i+8 <= len(src); i += 8 {
		x := binary.LittleEndian.Uint64(src[i:])
		low7 := x &^ high                                  // each byte reduced to 7 bits: no carry between bytes
		geA := low7 + (0x80-'A')*ones                      // high bit set where byte >= 'A'
		gtZ := low7 + (0x80-'Z'-1)*ones                    // high bit set where byte > 'Z'
		upper := geA &^ gtZ &^ x & high                    // A-Z, and not a byte >= 0x80
		binary.LittleEndian.PutUint64(dst[i:], x|upper>>2) // 0x80 >> 2 = 0x20
	}
	for ; i < len(src); i++ {
		c := src[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst[i] = c
	}
	return dst
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
