package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"pogo.hails.cc/internal/auth"
)

// costumes.LabelsJSON reproduces labels.json byte for byte, so it does not escape the
// literal "<" and ">" already sitting in the file's _comment blocks. This escapes at
// the injection site instead. Worth a test: the first attempt used "\u003c" in a Go
// double-quoted string, which the compiler turns straight back into "<", making the
// whole replacer a silent no-op.
func TestScriptSafeJSON(t *testing.T) {
	in := []byte(`{"note":"ends a block: </script><img src=x onerror=alert(1)>","amp":"a & b"}`)
	got := scriptSafeJSON(in)

	for _, bad := range []string{"<", ">", "&"} {
		if strings.Contains(got, bad) {
			t.Errorf("output still contains a raw %q, so it can break out of a <script> block:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, `\u003c`) {
		t.Errorf("expected \\u003c escapes in the output, got:\n%s", got)
	}

	// The escaped form must still be the same JSON value.
	var orig, escaped map[string]string
	if err := json.Unmarshal(in, &orig); err != nil {
		t.Fatalf("fixture is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(got), &escaped); err != nil {
		t.Fatalf("escaped output is not valid JSON: %v", err)
	}
	for k, v := range orig {
		if escaped[k] != v {
			t.Errorf("escaping changed the decoded value of %q:\n  want %q\n  got  %q", k, v, escaped[k])
		}
	}
}

// The ?next= allowlist. The backslash and control-character cases are the ones that
// defeated the previous check: url.Parse reports an empty Scheme and Host for all of
// them, but a browser resolves them off-site.
func TestSafeNextPath(t *testing.T) {
	allowed := []string{
		"/shinies",
		"/trainer/hails",
		"/shinies?costume=1",
		"/iv#results",
		"/",
	}
	for _, next := range allowed {
		if !safeNextPath(next) {
			t.Errorf("safeNextPath(%q) = false, want true (this is a normal same-site path)", next)
		}
	}

	refused := []string{
		"",                          // no target
		"//evil.com",                // scheme-relative
		"/\\evil.com",               // WHATWG resolves the backslash as a slash
		"\\\\evil.com",              // not even rooted
		"/\t/evil.com",              // tab is stripped, leaving //evil.com
		"/\n/evil.com",              // newline likewise
		"/\r/evil.com",              //
		"https://evil.com",          // absolute
		"http://evil.com",           //
		"//pogo.hails.app@evil.com", // userinfo trick
		"javascript:alert(1)",       // not a path at all
		"evil.com",                  // relative, resolves against the current directory
	}
	for _, next := range refused {
		if safeNextPath(next) {
			t.Errorf("safeNextPath(%q) = true, want false (this redirects off-site)", next)
		}
	}
}

// Locale strings are plain text. The banned set was chosen by measuring the shipped
// locales: 4475 strings, not one containing "<" or ">".
func TestSanitizeTranslation(t *testing.T) {
	refused := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`Caught " onfocus="alert(1)`, // breaks out of placeholder="..."
		`a < b`,
		`5 > 3`,
	}
	for _, in := range refused {
		if _, key := sanitizeTranslation(in); key == "" {
			t.Errorf("sanitizeTranslation(%q) accepted it, want refusal", in)
		}
	}

	// Apostrophes and ampersands stay: French alone uses 141 apostrophes.
	accepted := []string{
		"Nothing caught yet.",
		"Impossible de modifier l'objet",
		"Raids & Events",
		"Pokemon {name} not found.",
		"レイドが見つかりません",
	}
	for _, in := range accepted {
		if _, key := sanitizeTranslation(in); key != "" {
			t.Errorf("sanitizeTranslation(%q) refused it (%s), want accepted", in, key)
		}
	}

	// Control characters are stripped, not merely refused.
	got, key := sanitizeTranslation("hel\x00lo\x07")
	if key != "" {
		t.Errorf("control characters should be stripped, not refused, got key %q", key)
	}
	if got != "hello" {
		t.Errorf("sanitizeTranslation stripped to %q, want %q", got, "hello")
	}
}

// The trainer name is rendered into innerHTML as content and as an attribute value,
// and was previously checked for length alone.
func TestValidTrainerName(t *testing.T) {
	refused := []string{
		`<svg onload=a()>`,
		`<base href=//a.bc`,
		`" onfocus="x`,
		`Hails&Co`,
		`it's me`, // apostrophe is in the banned family for this field
	}
	for _, in := range refused {
		if validTrainerName(in) {
			t.Errorf("validTrainerName(%q) = true, want false", in)
		}
	}

	// Non-Latin names have to keep working: the check is by Unicode category.
	accepted := []string{
		"Hails",
		"Lady Hails",
		"Hails_92",
		"Zoe",
		"Ampharos-2",
		"ハイルズ",
		"Привет",
		"Ekaterina",
		"",
	}
	for _, in := range accepted {
		if !validTrainerName(in) {
			t.Errorf("validTrainerName(%q) = false, want true", in)
		}
	}
}

// Repeat positive trust between the same pair decays. uk_te_vote only makes a vote
// unique per lobby, so two accounts could stage raid after raid and commend each
// other every time, each one at full weight.
func TestPairDecayFactor(t *testing.T) {
	// Raiding repeatedly with the same people is normal, so the first few are free.
	for prior := 0; prior < trustPairFreeInteractions; prior++ {
		if got := pairDecayFactor(prior); got != 1.0 {
			t.Errorf("pairDecayFactor(%d) = %v, want 1.0: the first %d must not be penalised",
				prior, got, trustPairFreeInteractions)
		}
	}

	// Past that it halves each time.
	if got := pairDecayFactor(trustPairFreeInteractions); got != 0.5 {
		t.Errorf("first decayed event = %v, want 0.5", got)
	}
	if got := pairDecayFactor(trustPairFreeInteractions + 1); got != 0.25 {
		t.Errorf("second decayed event = %v, want 0.25", got)
	}

	// Never reaches zero, so the event is still recorded rather than silently dropped.
	if got := pairDecayFactor(500); got != trustPairDecayFloor {
		t.Errorf("pairDecayFactor(500) = %v, want the floor %v", got, trustPairDecayFloor)
	}

	// Monotonically non-increasing: no prior count may ever be worth more than a
	// smaller one, or farming would have a sweet spot.
	prev := pairDecayFactor(0)
	for prior := 1; prior < 60; prior++ {
		got := pairDecayFactor(prior)
		if got > prev {
			t.Fatalf("factor rose at prior=%d (%v after %v): farming would pay off there", prior, got, prev)
		}
		prev = got
	}

	// The whole point: a single pair's total contribution is bounded. Summing the
	// curve past the free window must converge to roughly one extra event's worth,
	// not grow without limit.
	var tail float64
	for prior := trustPairFreeInteractions; prior < 400; prior++ {
		tail += pairDecayFactor(prior)
	}
	if tail > float64(trustPairFreeInteractions)+25 {
		t.Errorf("tail sum %v is too large to call this a cap", tail)
	}
}

// staffRank drives every account-altering admin action. Before it existed the only
// check was on the superadmin's username, so a moderator could reset an
// administrator's password and log straight in as them.
func TestStaffRankOrdering(t *testing.T) {
	prev := auth.SuperadminUser
	auth.SuperadminUser = "hails"
	defer func() { auth.SuperadminUser = prev }()

	super := staffRank("hails", "user") // privilege comes from the env var, not the column
	admin := staffRank("someadmin", "admin")
	mod := staffRank("somemod", "moderator")
	user := staffRank("someuser", "user")
	tester := staffRank("sometester", "tester")

	if !(super > admin && admin > mod && mod > user) {
		t.Fatalf("ranks out of order: super=%d admin=%d mod=%d user=%d", super, admin, mod, user)
	}
	if tester != user {
		t.Errorf("tester rank = %d, want %d: tester is not a punitive role", tester, user)
	}
}

// The guard is "strictly outranks", so equal rank is refused too. A moderator must
// not be able to act on another moderator, nor an admin on another admin.
func TestStaffRankRefusesPeers(t *testing.T) {
	prev := auth.SuperadminUser
	auth.SuperadminUser = "hails"
	defer func() { auth.SuperadminUser = prev }()

	cases := []struct {
		name                   string
		callerName, callerRole string
		targetName, targetRole string
		wantAllowed            bool
	}{
		{"mod on admin", "m", "moderator", "a", "admin", false},
		{"mod on mod", "m", "moderator", "m2", "moderator", false},
		{"mod on superadmin", "m", "moderator", "hails", "user", false},
		{"mod on user", "m", "moderator", "u", "user", true},
		{"admin on admin", "a", "admin", "a2", "admin", false},
		{"admin on superadmin", "a", "admin", "hails", "user", false},
		{"admin on mod", "a", "admin", "m", "moderator", true},
		{"admin on user", "a", "admin", "u", "user", true},
		{"superadmin on admin", "hails", "user", "a", "admin", true},
		{"superadmin on mod", "hails", "user", "m", "moderator", true},
	}
	for _, tc := range cases {
		allowed := staffRank(tc.callerName, tc.callerRole) > staffRank(tc.targetName, tc.targetRole)
		if allowed != tc.wantAllowed {
			t.Errorf("%s: allowed = %v, want %v", tc.name, allowed, tc.wantAllowed)
		}
	}
}
