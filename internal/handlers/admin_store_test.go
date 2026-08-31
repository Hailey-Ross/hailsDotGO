package handlers

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The tag request queue is ordered with MySQL's FIELD(), and FIELD() answers 0 for
// a value it was not given. Zero sorts FIRST, so a status left out of the list does
// not fall to the bottom, it jumps the queue. 'revision' was missing and every
// request already sent back for changes sorted above the pending ones the queue
// exists to work through.
//
// This reads both files rather than asserting a literal, because the bug is a
// disagreement BETWEEN them and neither one is wrong on its own. Adding a value to
// the ENUM is the moment this breaks, and it is also the moment nothing else
// changes shape, so there is nothing else for a reviewer to notice.
func TestTagRequestOrderCoversEveryStatus(t *testing.T) {
	enum := enumValues(t, "../../schema.sql", "custom_tag_requests", "status")
	order := fieldOrder(t, "admin_store.go", "ctr.status")

	if len(enum) == 0 || len(order) == 0 {
		t.Fatalf("could not read both lists: enum=%v order=%v", enum, order)
	}
	for _, v := range enum {
		if !slices.Contains(order, v) {
			t.Errorf("status %q is in the ENUM but not in the FIELD() ordering, so it sorts FIRST; "+
				"add it to AdminTagRequestsList in the position it belongs", v)
		}
	}
	for _, v := range order {
		if !slices.Contains(enum, v) {
			t.Errorf("status %q is ordered but is not a value of the ENUM", v)
		}
	}

	// The order itself, not just its coverage. Open work above closed work, and the
	// two open states in the order staff act on them.
	want := []string{"pending", "revision", "approved", "rejected"}
	if !slices.Equal(order, want) {
		t.Errorf("ordering = %v, want %v", order, want)
	}
}

var (
	enumColumnRe = regexp.MustCompile(`(?m)^\s*(\w+)\s+ENUM\(([^)]*)\)`)
	fieldOrderRe = regexp.MustCompile(`FIELD\(([\w.]+)\s*,\s*([^)]*)\)`)
	sqlStringRe  = regexp.MustCompile(`'([^']*)'`)
)

// enumValues pulls one column's ENUM values out of a table's CREATE TABLE block.
func enumValues(t *testing.T, path, table, column string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	_, after, found := strings.Cut(string(src), "CREATE TABLE IF NOT EXISTS "+table+" (")
	if !found {
		t.Fatalf("no CREATE TABLE for %s in %s", table, path)
	}
	block, _, _ := strings.Cut(after, "\n) ENGINE=")
	for _, m := range enumColumnRe.FindAllStringSubmatch(block, -1) {
		if m[1] == column {
			return sqlLiterals(m[2])
		}
	}
	t.Fatalf("no ENUM column %s on %s", column, table)
	return nil
}

// fieldOrder pulls the value list out of the FIELD() call on a given column.
func fieldOrder(t *testing.T, path, column string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, m := range fieldOrderRe.FindAllStringSubmatch(string(src), -1) {
		if m[1] == column {
			return sqlLiterals(m[2])
		}
	}
	t.Fatalf("no FIELD(%s, ...) in %s", column, path)
	return nil
}

func sqlLiterals(s string) []string {
	var out []string
	for _, m := range sqlStringRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
