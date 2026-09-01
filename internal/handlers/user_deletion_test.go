package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A marked account must vanish from every read path, and SQL enforces none of that:
// the rule lives as a hand-written clause in twenty separate queries. A missed one
// does not error, it renders a ghost trainer whose account is supposed to be gone.
//
// So the two clauses are pinned TOGETHER. Every `disabled = 0` must be followed by
// `deleted_at IS NULL`, which makes the invariant checkable at the source level
// instead of relying on whoever adds query twenty-one to remember. This is the same
// shape of bug as the FIELD()/ENUM mismatch: a rule living in many places with
// nothing checking that they agree.
func TestNoUserLookupForgetsTheDeletedClause(t *testing.T) {
	// Go's regexp has no negative lookahead, so this finds every disabled check and
	// then inspects what follows each one.
	clause := regexp.MustCompile(`(?:\w+\.)?disabled = 0`)
	follows := regexp.MustCompile(`^ AND (?:\w+\.)?deleted_at IS NULL`)

	files, err := filepath.Glob(filepath.Join("..", "handlers", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := stripLineComments(strings.ReplaceAll(string(raw), "\r\n", "\n"))
		for _, m := range clause.FindAllStringIndex(src, -1) {
			if follows.MatchString(src[m[1]:]) {
				continue
			}
			line := strings.Count(src[:m[0]], "\n") + 1
			t.Errorf("%s:%d: %q excludes disabled accounts but not deleted ones, so a marked account would still show here",
				filepath.Base(f), line, strings.TrimSpace(src[m[0]:m[1]]))
		}
		found += strings.Count(src, "disabled = 0")
	}
	// A guard on the guard: if the clause is ever renamed wholesale, the loop above
	// silently passes over zero matches and this test stops meaning anything.
	if found < 15 {
		t.Fatalf("only %d user visibility clauses found, expected at least 15; has the clause been renamed?", found)
	}
}

// stripLineComments blanks the contents of // comments while preserving line numbers,
// so prose ABOUT the clause does not read as a query using it. Safe here because the
// queries live in backtick raw strings, which never contain a // of their own.
func stripLineComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if j := strings.Index(line, "//"); j >= 0 {
			lines[i] = line[:j]
		}
	}
	return strings.Join(lines, "\n")
}

// Signing in is the path that must not be missed, because it is the one that would
// hand a marked account back its access rather than merely showing it to others.
func TestAuthPathsExcludeMarkedAccounts(t *testing.T) {
	for _, c := range []struct{ file, what string }{
		{"auth.go", "web login"},
		{"mobile_auth.go", "mobile login"},
		{"passwordreset.go", "password reset"},
	} {
		raw, err := os.ReadFile(filepath.Join("..", "handlers", c.file))
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		if !strings.Contains(string(raw), "deleted_at IS NULL") {
			t.Errorf("%s (%s) has no deleted_at check, so a marked account could still get in", c.file, c.what)
		}
	}

	// Session resolution lives in the auth package and is what drops a marked
	// account's EXISTING sessions rather than only blocking new sign ins.
	raw, err := os.ReadFile(filepath.Join("..", "auth", "session.go"))
	if err != nil {
		t.Fatalf("read session.go: %v", err)
	}
	if !strings.Contains(string(raw), "u.deleted_at IS NULL") {
		t.Error("auth.GetSession does not exclude marked accounts, so existing sessions would survive deletion")
	}
}

// The retention window is the whole feature. A stray edit turning 90 days into 90
// hours would quietly destroy data early and nothing else would notice.
func TestRetentionWindowIsNinetyDays(t *testing.T) {
	if days := int(userPurgeAfter.Hours() / 24); days != 90 {
		t.Errorf("retention window is %d days, want 90", days)
	}
}

// Marking must be idempotent in the way that matters: re-marking an already marked
// account must not restart its clock, or an account could be kept alive forever by
// repeated deletions.
func TestMarkIsScopedToLiveAccounts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "handlers", "user_deletion.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(src, "func (h *Handlers) markUserDeleted")
	if i < 0 {
		t.Fatal("markUserDeleted is gone")
	}
	body := src[i:min(i+900, len(src))]
	if !strings.Contains(body, "AND deleted_at IS NULL") {
		t.Error("markUserDeleted does not scope its UPDATE to unmarked rows, so re-deleting an account restarts its retention window")
	}
	if !strings.Contains(body, "DELETE FROM sessions") {
		t.Error("markUserDeleted does not drop the account's sessions")
	}
}

// The purge is the only thing in the app that destroys a user row, and it must only
// ever reach rows that are both marked AND past the window.
func TestPurgeOnlyTouchesExpiredMarks(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "handlers", "user_deletion.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.Contains(src, "WHERE deleted_at IS NOT NULL AND deleted_at < ?") {
		t.Error("the purge sweep is not scoped to marked rows past the cutoff")
	}

	// Exactly one DELETE FROM users in the whole package, and it must live here.
	files, _ := filepath.Glob(filepath.Join("..", "handlers", "*.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || filepath.Base(f) == "user_deletion.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "DELETE FROM users") {
			t.Errorf("%s destroys a user row; the purge in user_deletion.go must be the only place that does", filepath.Base(f))
		}
	}
}

// Restoring must only ever lift a mark. Without the clause the UPDATE would report a
// row for a live account too, and the panel would announce it had undone a deletion
// that had never happened.
func TestRestoreOnlyLiftsAMark(t *testing.T) {
	body := funcBody(t, "user_deletion.go", "func (h *Handlers) restoreUser", 700)
	if !strings.Contains(body, "deleted_at = NULL") {
		t.Fatal("restoreUser does not clear the mark")
	}
	if !strings.Contains(body, "AND deleted_at IS NOT NULL") {
		t.Error("restoreUser is not scoped to marked rows, so it would report success on a live account")
	}
}

// The purge is reachable from a button now, not only from the sweep, so it is handed
// an id by a browser. The mark is the thing that makes such an id safe to act on, and
// the clause is what stops a live account being destroyed by a mistyped or forged id.
func TestPurgeRefusesAnUnmarkedAccount(t *testing.T) {
	body := funcBody(t, "user_deletion.go", "func (h *Handlers) purgeOneUser", 1600)
	if !strings.Contains(body, "AND deleted_at IS NOT NULL") {
		t.Error("purgeOneUser can destroy a live account: its DELETE is not scoped to marked rows")
	}
	if !strings.Contains(body, "errUserNotMarked") {
		t.Error("purgeOneUser does not report an unmarked row, so the caller cannot tell a refusal from a success")
	}
}

// Erasing skips the retention window, which makes it the one deletion action with no
// undo behind it. It must therefore keep every guard the delete has: superadmin only,
// staff protection, and the typed username. Restoring keeps the first two; it needs no
// typed username because it destroys nothing.
func TestEraseAndRestoreKeepTheDeleteGuards(t *testing.T) {
	purge := funcBody(t, "admin.go", "func (h *Handlers) AdminPurgeUser", 3000)
	for _, guard := range []string{"IsSuperAdmin()", "h.mayActOn(", "confirmMatchesUsername("} {
		if !strings.Contains(purge, guard) {
			t.Errorf("AdminPurgeUser is missing %s, and it is the action that cannot be undone", guard)
		}
	}
	restore := funcBody(t, "admin.go", "func (h *Handlers) AdminRestoreUser", 2000)
	for _, guard := range []string{"IsSuperAdmin()", "h.mayActOn("} {
		if !strings.Contains(restore, guard) {
			t.Errorf("AdminRestoreUser is missing %s", guard)
		}
	}

	// Both routes, on both mounts. A handler gated only by the check it does to itself
	// is one re-mount away from being open.
	raw, err := os.ReadFile(filepath.Join("..", "server", "server.go"))
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, want := range []string{
		`Post("/admin/users/{id}/purge", h.RequireSuperAdmin(h.AdminPurgeUser))`,
		`Post("/admin/users/{id}/restore", h.RequireSuperAdmin(h.AdminRestoreUser))`,
		`Post("/users/{id}/purge", h.RequireSuperAdminAPI(h.AdminPurgeUser))`,
		`Post("/users/{id}/restore", h.RequireSuperAdminAPI(h.AdminRestoreUser))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("server.go does not mount %s behind the superadmin gate", want)
		}
	}
}

// funcBody returns the source of one function, or as much of it as the given budget
// allows. Enough for asserting that a statement or a guard is present, which is all
// these tests need: the alternative is a live database.
func funcBody(t *testing.T, file, decl string, budget int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "handlers", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	src := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("%s is gone from %s", decl, file)
	}
	return src[i:min(i+budget, len(src))]
}
