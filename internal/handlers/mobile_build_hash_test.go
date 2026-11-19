package handlers

import "testing"

func TestValidSHA256(t *testing.T) {
	good := "72e61eea091ae9b2830a133d033587e8b07868bb84df31e1a80b287d20258f73"
	cases := map[string]bool{
		good:                  true,
		"":                    false,
		good[:63]:             false,
		good + "0":            false,
		"72E61EEA" + good[8:]: false, // uppercase is refused, as the handoff specifies
		"g2e61eea" + good[8:]: false,
		" " + good[1:]:        false,
	}
	for in, want := range cases {
		if got := validSHA256(in); got != want {
			t.Errorf("validSHA256(%q) = %v, want %v", in, got, want)
		}
	}
}

// The stored pair ties a hash to its build, so a hash for one build can never be shown
// next to another.
func TestParseBuildHash(t *testing.T) {
	sha := "72e61eea091ae9b2830a133d033587e8b07868bb84df31e1a80b287d20258f73"
	if n, s, ok := parseBuildHash("97:" + sha); !ok || n != 97 || s != sha {
		t.Fatalf("parseBuildHash(97:sha) = %d, %q, %v", n, s, ok)
	}
	for _, bad := range []string{"", sha, "97:", ":" + sha, "0:" + sha, "x:" + sha, "97:" + sha[:10]} {
		if _, _, ok := parseBuildHash(bad); ok {
			t.Errorf("parseBuildHash(%q) accepted a malformed value", bad)
		}
	}
}

// Build 97's fallback hash must itself be a valid fingerprint.
func TestKnownBuildScansAreValid(t *testing.T) {
	for b, sha := range knownBuildScans {
		if !validBuildNumber(b) || !validSHA256(sha) {
			t.Errorf("knownBuildScans[%d] = %q is not a valid build and hash", b, sha)
		}
	}
}
