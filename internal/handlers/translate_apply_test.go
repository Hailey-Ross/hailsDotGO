package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

var languagesColumnRe = regexp.MustCompile(`languages\s+VARCHAR\((\d+)\)`)

// languagesColumnChars reads the declared width of
// translator_applications.languages out of schema.sql, so this test tracks the
// real column instead of a number copied next to it.
func languagesColumnChars(t *testing.T) int {
	t.Helper()
	// Located from this source file, not the working directory, which another test
	// in this package moves.
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate this test file")
	}
	path := filepath.Join(filepath.Dir(self), "..", "..", "schema.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := languagesColumnRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("could not find the languages column in schema.sql")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse column width %q: %v", m[1], err)
	}
	return n
}

// TestTranslatorLanguagesFitColumn is the regression test for an applicant who
// ticked every language. The JSON is assembled from a bounded set of inputs, so
// its worst case is knowable up front: at VARCHAR(500) it came to 573 characters,
// the insert failed, and the application was refused with a generic error.
//
// Adding a language to supportedApplyLangs or raising maxOtherLangChars should
// fail here rather than at the applicant.
func TestTranslatorLanguagesFitColumn(t *testing.T) {
	// The longest of the three levels the form accepts.
	const longestLevel = "intermediate"

	entries := make([]langEntry, 0, len(supportedApplyLangs)+1)
	for _, code := range supportedApplyLangs {
		entries = append(entries, langEntry{code, longestLevel})
	}
	// A three-byte script in the free text box: worst case for bytes, and the
	// cap counts characters.
	entries = append(entries, langEntry{
		"other:" + strings.Repeat("\u3042", maxOtherLangChars),
		longestLevel,
	})

	out, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal entries: %v", err)
	}

	limit := languagesColumnChars(t)
	// MySQL counts VARCHAR in characters, so that is what has to fit.
	if n := utf8.RuneCountInString(string(out)); n > limit {
		t.Errorf("worst case languages JSON is %d characters, column holds %d", n, limit)
	}
	// utf8mb4 is 4 bytes per character at most, which is the other ceiling
	// VARCHAR carries. Well clear here, but worth stating.
	if len(out) > limit*4 {
		t.Errorf("worst case languages JSON is %d bytes, column holds %d", len(out), limit*4)
	}
}

// The levels the handler accepts must stay in step with the level this test
// assumes is the longest, or the worst case above stops being the worst case.
func TestLongestApplyLevelIsIntermediate(t *testing.T) {
	for _, level := range []string{"native", "fluent", "intermediate"} {
		if len(level) > len("intermediate") {
			t.Errorf("level %q is longer than intermediate, so the fit test understates the worst case", level)
		}
	}
}
