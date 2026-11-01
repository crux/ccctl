package search

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

// The fast paths must agree with the literal regexp they replace.
func TestMatcherAgreesWithRegexp(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("abcABC xyzXYZ-_.\"\\äÄ€\nzZ")
	randText := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		return b.String()
	}

	patterns := []string{"a", "z", "abc", "ABC", "aBc-x", "zz", "xYz", "z.\"", "\\", "-", "äB", "€c", "xyz xyz", "cZa", "äbcA", "Äxyz"}
	for _, p := range patterns {
		for _, cs := range []bool{false, true} {
			m, err := newMatcher(p, cs)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2000; i++ {
				hay := []byte(randText(rng.Intn(40)))
				if got, want := m.contains(hay), m.re.Match(hay); got != want {
					t.Fatalf("pattern %q caseSensitive=%v hay %q: contains=%v, regexp=%v", p, cs, hay, got, want)
				}
			}
		}
	}
}

func TestContainsFoldASCIIPositions(t *testing.T) {
	old := foldChunk
	foldChunk = 16 // small windows: matches cross window borders
	defer func() { foldChunk = old }()

	for _, needle := range []string{"makefile", "zebra", "x"} {
		lower := []byte(needle)
		upper := strings.ToUpper(needle[:1]) + needle[1:]
		for offset := 0; offset < 40; offset++ {
			if !containsFoldASCII([]byte(strings.Repeat("e", offset)+upper), lower) {
				t.Errorf("%q at offset %d (end of input) not found", needle, offset)
			}
			if !containsFoldASCII([]byte(upper+strings.Repeat("e", offset)), lower) {
				t.Errorf("%q at the start, %d bytes after, not found", needle, offset)
			}
		}
		if len(needle) > 1 && containsFoldASCII([]byte(strings.Repeat(needle[:len(needle)-1]+" ", 10)), lower) {
			t.Errorf("%q: found a match that is not there", needle)
		}
	}
}

// The 8-byte lowering must equal the plain byte-by-byte rule for every
// byte value at every position in the 8-byte word, and for odd tails.
func TestLowerASCIIMatchesPlainRule(t *testing.T) {
	plain := func(c byte) byte {
		if 'A' <= c && c <= 'Z' {
			return c + 'a' - 'A'
		}
		return c
	}
	for pos := 0; pos < 8; pos++ {
		for v := 0; v < 256; v++ {
			src := []byte("0aZ@[`{\xff")[:8]
			src[pos] = byte(v)
			got := lowerASCII(nil, src)
			for i := range src {
				if got[i] != plain(src[i]) {
					t.Fatalf("byte %#x at %d: got %#x, want %#x (word % x)", src[i], i, got[i], plain(src[i]), src)
				}
			}
		}
	}
	for n := 0; n < 20; n++ {
		src := []byte(strings.Repeat("AbZ\xc3\x84[", 4))[:n]
		got := lowerASCII(nil, src)
		for i := range src {
			if got[i] != plain(src[i]) {
				t.Fatalf("length %d, index %d: got %#x", n, i, got[i])
			}
		}
	}
}

var benchHay = []byte(strings.Repeat(`{"type":"assistant","message":{"content":"Some Text About Builds and nothing else"}}`+"\n", 1<<14))

func BenchmarkContainsFoldASCII(b *testing.B) {
	needle := []byte("makefile")
	b.SetBytes(int64(len(benchHay)))
	for i := 0; i < b.N; i++ {
		containsFoldASCII(benchHay, needle)
	}
}

func BenchmarkRegexpFold(b *testing.B) {
	m, _ := newMatcher("makefile", false)
	b.SetBytes(int64(len(benchHay)))
	for i := 0; i < b.N; i++ {
		m.re.Match(benchHay)
	}
}

func BenchmarkLowerASCII(b *testing.B) {
	dst := make([]byte, len(benchHay))
	b.SetBytes(int64(len(benchHay)))
	for i := 0; i < b.N; i++ {
		dst = lowerASCII(dst, benchHay)
	}
}

func BenchmarkIndexLowered(b *testing.B) {
	lowered := lowerASCII(nil, benchHay)
	needle := []byte("makefile")
	b.SetBytes(int64(len(benchHay)))
	for i := 0; i < b.N; i++ {
		bytes.Index(lowered, needle)
	}
}

func TestLongestASCIIRun(t *testing.T) {
	for in, want := range map[string]string{"Lüsebrink": "sebrink", "äb": "b", "abc": "abc", "ü": "", "Größe der Datei": "e der Datei"} {
		if got := longestASCIIRun(in); got != want {
			t.Errorf("longestASCIIRun(%q) = %q, want %q", in, got, want)
		}
	}
}
