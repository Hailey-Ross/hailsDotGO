package handlers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncRunesKeepsValidUTF8 is the regression test for the 500 that started
// this: details[:4000] on Japanese input cut one byte inside a three byte rune,
// MySQL rejected the invalid utf8mb4 with error 1366, and the insert failed.
func TestTruncRunesKeepsValidUTF8(t *testing.T) {
	in := strings.Repeat("\u3042", 5000) // 5000 runes, 15000 bytes
	got := truncRunes(in, 4000)

	if n := utf8.RuneCountInString(got); n != 4000 {
		t.Errorf("rune count = %d, want 4000", n)
	}
	if !utf8.ValidString(got) {
		t.Error("result is not valid UTF-8, which is the whole bug")
	}
	// The byte slice this replaced fails the check above. Assert that directly so
	// nobody quietly reverts to it.
	if utf8.ValidString(in[:4000]) {
		t.Error("in[:4000] is valid UTF-8, so this test no longer proves anything")
	}
}

func TestTruncRunesBoundaries(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty", "", 10, ""},
		{"shorter than limit", "hello", 10, "hello"},
		{"exactly the limit", "hello", 5, "hello"},
		{"one over the limit", "hello!", 5, "hello"},
		{"zero limit", "hello", 0, ""},
		{"multibyte under limit", "\u3042\u3044\u3046", 5, "\u3042\u3044\u3046"},
		{"multibyte over limit", "\u3042\u3044\u3046", 2, "\u3042\u3044"},
		{"emoji counts as one rune", "\U0001F600\U0001F600\U0001F600", 2, "\U0001F600\U0001F600"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncRunes(tc.in, tc.n); got != tc.want {
				t.Errorf("truncRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

// TestUpperFirstKeepsValidUTF8 covers the sibling of the truncation bug: the
// shape strings.ToUpper(s[:1]) + s[1:] splits one byte off a multi-byte first
// character and hands back something that is not valid UTF-8.
func TestUpperFirstKeepsValidUTF8(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"ascii", "pikachu", "Pikachu"},
		{"already upper", "Pikachu", "Pikachu"},
		{"single letter", "a", "A"},
		{"digit stays", "25a", "25a"},
		{"japanese is left alone", "\u3042\u3044", "\u3042\u3044"},
		{"accented first letter", "\u00e9lectrique", "\u00c9lectrique"},
		{"emoji first", "\U0001F600ok", "\U0001F600ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := upperFirst(tc.in)
			if got != tc.want {
				t.Errorf("upperFirst(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("upperFirst(%q) produced invalid UTF-8", tc.in)
			}
		})
	}

	// The shape this replaced fails on the very first multi-byte case above.
	if in := "\u3042\u3044"; utf8.ValidString(strings.ToUpper(in[:1]) + in[1:]) {
		t.Error("the old byte-slicing shape is valid UTF-8, so this test no longer proves anything")
	}
}

// readHandlerSource returns one file from this package as text, for the assertions
// that are about the shape of the code rather than its behaviour.
//
// This package has no database harness, so a handler whose whole job is a query
// cannot be driven end to end here. Where the thing worth guarding is a clause or
// a call that must not be dropped, reading the source is the honest way to guard
// it, and it is the same approach internal/server takes for the route table.
func readHandlerSource(t *testing.T, name string) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate this test file")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(self), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}
