package migrate

import (
	"path/filepath"
	"strings"
	"testing"
)

const sampleSQL = `-- header comment, ignored

-- 1. First section
ALTER TABLE users ADD COLUMN a INT;
ALTER TABLE users ADD COLUMN b INT;

-- 2. Second section (2026-06-28)
-- an inline note that should be stripped
CREATE TABLE IF NOT EXISTS foo (
  id INT
);
INSERT IGNORE INTO foo (id) VALUES (1);
`

func TestParse(t *testing.T) {
	secs, err := Parse(sampleSQL)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(secs) != 2 {
		t.Fatalf("want 2 sections, got %d", len(secs))
	}
	if secs[0].Num != 1 || secs[0].Title != "First section" {
		t.Errorf("section 1 header wrong: %+v", secs[0])
	}
	if len(secs[0].Stmts) != 2 {
		t.Errorf("section 1: want 2 statements, got %d: %q", len(secs[0].Stmts), secs[0].Stmts)
	}
	if secs[1].Title != "Second section (2026-06-28)" {
		t.Errorf("section 2 title wrong: %q", secs[1].Title)
	}
	// The multi-line CREATE plus the INSERT collapse to exactly 2 statements,
	// with the comment line stripped.
	if len(secs[1].Stmts) != 2 {
		t.Errorf("section 2: want 2 statements, got %d: %q", len(secs[1].Stmts), secs[1].Stmts)
	}
	for _, s := range secs[1].Stmts {
		if strings.Contains(s, "inline note") {
			t.Errorf("comment leaked into statement: %q", s)
		}
	}
}

func TestParseRejectsGaps(t *testing.T) {
	_, err := Parse("-- 1. ok\nSELECT 1;\n-- 3. skipped two\nSELECT 1;\n")
	if err == nil {
		t.Fatal("expected an error for non-contiguous section numbers")
	}
}

// TestParseRealFile guards the actual migrate.sql: it must parse, be contiguous,
// and every section must contain at least one statement.
func TestParseRealFile(t *testing.T) {
	secs, err := ParseFile(filepath.Join("..", "..", "migrate.sql"))
	if err != nil {
		t.Fatalf("ParseFile(migrate.sql): %v", err)
	}
	if len(secs) < 40 {
		t.Fatalf("expected at least 40 sections, got %d", len(secs))
	}
	for _, s := range secs {
		if len(s.Stmts) == 0 {
			t.Errorf("section %d (%s) parsed to zero statements", s.Num, s.Title)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"0.1.3a":    "v0.1.3a",
		"V0.1.3A":   "v0.1.3a",
		" v0.1.3a ": "v0.1.3a",
	}
	for in, want := range cases {
		if got := NormalizeVersion(in); got != want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKnownVersionsOrdered(t *testing.T) {
	vs := KnownVersions()
	for i := 1; i < len(vs); i++ {
		if BaselineVersions[vs[i-1]] > BaselineVersions[vs[i]] {
			t.Errorf("KnownVersions not ascending by section: %v", vs)
		}
	}
}
