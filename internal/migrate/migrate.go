// Package migrate is the upgrade engine for existing hailsDotGO installs.
//
// migrate.sql at the repo root is the single source of truth: it is a list of
// numbered sections, each headed `-- N. Title (date)`. This package parses that
// file into ordered sections, tracks which have been applied in a
// schema_migrations table, and applies only the pending ones.
//
// Adding a future migration is just appending a new `-- N. ...` section to
// migrate.sql (and mirroring the change in schema.sql for fresh installs). The
// runner and the cmd/migrate CLI pick it up automatically with no code change.
package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// Section is one numbered block of migrate.sql.
type Section struct {
	Num   int
	Title string
	Stmts []string // executable statements, comments and blanks stripped
}

// BaselineVersions maps a released version to the highest migrate.sql section
// that was already applied at that release. It lets an existing, untracked
// install record everything up to its current version without re-running it.
//
// v0.1.7b is the migration baseline floor: releases before it were intentionally
// removed, so the migrate tool only recognises v0.1.7b as a `-from` target and
// migrations run forward from here only. An install older than v0.1.7b is no
// longer upgradable in place; it reinstalls from schema.sql (which seeds the full
// history) and carries on from there. Keep this in step as we tag: add the new
// version each release, mapped to its highest applied section.
var BaselineVersions = map[string]int{
	"v0.1.7b": 43,
	"v0.1.7c": 43, // no schema change: the collection rework is UI and date handling only
}

// headerRe matches a section header line like "-- 39. Bug report system (2026-06-28)".
var headerRe = regexp.MustCompile(`^--\s*(\d+)\.\s*(.*)$`)

// NormalizeVersion lowercases and ensures a leading "v" so "0.1.3a", "V0.1.3A",
// and "v0.1.3a" all resolve the same.
func NormalizeVersion(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// KnownVersions returns the recognised version strings, newest section last.
func KnownVersions() []string {
	out := make([]string, 0, len(BaselineVersions))
	for v := range BaselineVersions {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return BaselineVersions[out[i]] < BaselineVersions[out[j]] })
	return out
}

// ParseFile reads and parses a migrate.sql file from disk.
func ParseFile(path string) ([]Section, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(string(b))
}

// Parse splits migrate.sql content into ordered sections. Statements inside a
// section are separated on ';'; full-line comments and blank lines are dropped.
func Parse(content string) ([]Section, error) {
	var sections []Section
	var cur *Section
	var buf []string

	flush := func() {
		if cur == nil {
			return
		}
		cur.Stmts = splitStatements(buf)
		sections = append(sections, *cur)
		cur = nil
		buf = nil
	}

	for _, line := range strings.Split(content, "\n") {
		if m := headerRe.FindStringSubmatch(line); m != nil {
			flush()
			num, _ := strconv.Atoi(m[1])
			cur = &Section{Num: num, Title: strings.TrimSpace(m[2])}
			continue
		}
		if cur != nil {
			buf = append(buf, line)
		}
	}
	flush()

	if len(sections) == 0 {
		return nil, errors.New("no numbered sections found (expected lines like '-- 1. Title')")
	}
	// Enforce a strictly increasing, gap-free sequence so a typo in a header is
	// caught instead of silently skipping a migration.
	for i, s := range sections {
		if s.Num != i+1 {
			return nil, fmt.Errorf("section numbering is not contiguous: expected %d, found %d (%q)", i+1, s.Num, s.Title)
		}
	}
	return sections, nil
}

// splitStatements drops comment-only and blank lines, then splits the remainder
// on ';' into trimmed, non-empty statements.
func splitStatements(lines []string) []string {
	var sb strings.Builder
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	var out []string
	for _, raw := range strings.Split(sb.String(), ";") {
		if s := strings.TrimSpace(raw); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// EnsureTable creates the schema_migrations tracking table if it is missing.
func EnsureTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		section    INT UNSIGNED NOT NULL,
		name       VARCHAR(160) NOT NULL DEFAULT '',
		applied_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (section)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	return err
}

// AppliedSections returns the set of section numbers already recorded.
func AppliedSections(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT section FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var n int
		if rows.Scan(&n) == nil {
			out[n] = true
		}
	}
	return out, rows.Err()
}

// TableExists reports whether a table exists in the current database.
func TableExists(db *sql.DB, table string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
		table,
	).Scan(&n)
	return n > 0, err
}

// record marks a section applied. INSERT IGNORE keeps it safe to call twice.
func record(db *sql.DB, num int, title string) error {
	_, err := db.Exec(`INSERT IGNORE INTO schema_migrations (section, name) VALUES (?, ?)`, num, title)
	return err
}

// RecordThrough marks every section up to and including `through` as applied
// without running it. Used to baseline an existing install at its known version.
func RecordThrough(db *sql.DB, through int, sections []Section) error {
	for _, s := range sections {
		if s.Num > through {
			break
		}
		if err := record(db, s.Num, s.Title); err != nil {
			return err
		}
	}
	return nil
}

// ApplySection runs one section's statements, tolerating "already applied"
// errors so partially-applied or re-run sections succeed. It returns the number
// of statements that were skipped as already-applied.
func ApplySection(db *sql.DB, s Section) (skipped int, err error) {
	for _, stmt := range s.Stmts {
		if _, e := db.Exec(stmt); e != nil {
			if isAlreadyApplied(e) {
				skipped++
				continue
			}
			return skipped, fmt.Errorf("section %d (%s): %w\n  statement: %s", s.Num, s.Title, e, firstLine(stmt))
		}
	}
	if err := record(db, s.Num, s.Title); err != nil {
		return skipped, fmt.Errorf("record section %d: %w", s.Num, err)
	}
	return skipped, nil
}

// alreadyAppliedCodes are MySQL errors that mean the change is already in place,
// so re-running the statement is a no-op rather than a real failure.
var alreadyAppliedCodes = map[uint16]bool{
	1050: true, // ER_TABLE_EXISTS_ERROR
	1060: true, // ER_DUP_FIELDNAME (duplicate column)
	1061: true, // ER_DUP_KEYNAME (duplicate index/key)
	1091: true, // ER_CANT_DROP_FIELD_OR_KEY (already dropped)
	1022: true, // ER_DUP_KEY
	1826: true, // ER_DUP_CONSTRAINT_NAME (duplicate foreign key name)
}

func isAlreadyApplied(err error) bool {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return alreadyAppliedCodes[me.Number]
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " ..."
	}
	return strings.TrimSpace(s)
}
