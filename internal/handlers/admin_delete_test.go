package handlers

import (
	"testing"

	"pogo.hails.cc/internal/auth"
)

// The typed confirmation is the only thing standing between a mis-click and an
// unrecoverable delete, so it has to be strict. Anything that would let a caller
// confirm without actually reading the username off the screen is a bug.
func TestConfirmMatchesUsername(t *testing.T) {
	cases := []struct {
		name   string
		typed  string
		target string
		want   bool
	}{
		{"exact match", "trainer_red", "trainer_red", true},
		{"surrounding whitespace is forgiven", "  trainer_red  ", "trainer_red", true},
		{"wrong case", "Trainer_Red", "trainer_red", false},
		{"empty", "", "trainer_red", false},
		{"whitespace only", "   ", "trainer_red", false},
		{"prefix", "trainer", "trainer_red", false},
		{"suffix", "red", "trainer_red", false},
		{"extra character", "trainer_red2", "trainer_red", false},
		{"a different account", "trainer_blue", "trainer_red", false},
		{"inner whitespace is not trimmed", "trainer red", "trainer_red", false},
	}
	for _, tc := range cases {
		if got := confirmMatchesUsername(tc.typed, tc.target); got != tc.want {
			t.Errorf("%s: confirmMatchesUsername(%q, %q) = %v, want %v",
				tc.name, tc.typed, tc.target, got, tc.want)
		}
	}
}

// A target with an empty username would otherwise be deletable by typing nothing,
// which is exactly the case a corrupt or half-written row would produce.
func TestConfirmMatchesUsernameRefusesEmptyTarget(t *testing.T) {
	if confirmMatchesUsername("", "") {
		t.Error("confirmMatchesUsername(\"\", \"\") = true, want false: an empty confirmation must never match")
	}
	if confirmMatchesUsername("   ", "") {
		t.Error("whitespace matched an empty target username, want false")
	}
}

// Permanent deletion is gated by the same staffRank comparison as every other
// account-altering action. The tie is what stops the superadmin deleting
// themselves, and it is the only thing that does so besides the explicit id check.
func TestDeleteRankGate(t *testing.T) {
	prev := auth.SuperadminUser
	auth.SuperadminUser = "hails"
	defer func() { auth.SuperadminUser = prev }()

	cases := []struct {
		name                   string
		callerName, callerRole string
		targetName, targetRole string
		wantAllowed            bool
	}{
		// Lady Hails chose "anyone but self", so staff below superadmin are targets.
		{"superadmin on admin", "hails", "user", "a", "admin", true},
		{"superadmin on moderator", "hails", "user", "m", "moderator", true},
		{"superadmin on tester", "hails", "user", "t", "tester", true},
		{"superadmin on user", "hails", "user", "u", "user", true},
		{"superadmin on themselves", "hails", "user", "hails", "user", false},
		// Nobody below superadmin reaches the handler, but the gate refuses them anyway.
		{"admin on user", "a", "admin", "u", "user", true},
		{"admin on superadmin", "a", "admin", "hails", "user", false},
		{"moderator on superadmin", "m", "moderator", "hails", "user", false},
	}
	for _, tc := range cases {
		allowed := staffRank(tc.callerName, tc.callerRole) > staffRank(tc.targetName, tc.targetRole)
		if allowed != tc.wantAllowed {
			t.Errorf("%s: allowed = %v, want %v", tc.name, allowed, tc.wantAllowed)
		}
	}
}

// The superadmin is identified by username, not by the role column, so an account
// that merely holds role='admin' must not inherit deletion rights when the env var
// is unset. Without a configured superadmin nobody outranks an admin.
func TestDeleteGateWithNoSuperadminConfigured(t *testing.T) {
	prev := auth.SuperadminUser
	auth.SuperadminUser = ""
	defer func() { auth.SuperadminUser = prev }()

	if staffRank("hails", "user") != 0 {
		t.Error("an unset SUPERADMIN_USER still granted rank by username")
	}
	if staffRank("a", "admin") > staffRank("a2", "admin") {
		t.Error("one admin outranked another with no superadmin configured")
	}
}
