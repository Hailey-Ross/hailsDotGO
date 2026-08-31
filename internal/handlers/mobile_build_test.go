package handlers

import "testing"

func TestParseBuildNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"39", 39, true},
		{" 39 ", 39, true},
		{"1", 1, true},
		{"1000000", 1000000, true},
		// An unset setting reads as the empty string, and the row we would write
		// for it reads as "0". Neither is a build anyone can be behind.
		{"", 0, false},
		{"0", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{"1e3", 0, false},
		{"39.0", 0, false},
		// A fat fingered entry: accepted, it would tell every install it is stale
		// with no way for the app to tell the difference.
		{"1000001", 0, false},
	}
	for _, c := range cases {
		got, ok := parseBuildNumber(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseBuildNumber(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestBuildTokenOK(t *testing.T) {
	const secret = "s3cr3t"
	cases := []struct {
		name   string
		header string
		secret string
		want   bool
	}{
		{"match", "Bearer " + secret, secret, true},
		{"trailing space", "Bearer " + secret + " ", secret, true},
		{"wrong token", "Bearer nope", secret, false},
		{"no bearer prefix", secret, secret, false},
		{"empty header", "", secret, false},
		{"prefix only", "Bearer ", secret, false},
		// The important one: with no MOBILE_BUILD_TOKEN configured, nothing at all
		// may authenticate, including an empty or bare-prefix header that would
		// otherwise compare equal to the empty secret.
		{"unset secret, empty header", "", "", false},
		{"unset secret, prefix only", "Bearer ", "", false},
		{"unset secret, any token", "Bearer anything", "", false},
	}
	for _, c := range cases {
		if got := buildTokenOK(c.header, c.secret); got != c.want {
			t.Errorf("%s: buildTokenOK(%q, %q) = %v, want %v", c.name, c.header, c.secret, got, c.want)
		}
	}
}

func TestValidBuildNumber(t *testing.T) {
	for _, n := range []int{1, 39, 1000000} {
		if !validBuildNumber(n) {
			t.Errorf("validBuildNumber(%d) = false, want true", n)
		}
	}
	for _, n := range []int{0, -1, 1000001} {
		if validBuildNumber(n) {
			t.Errorf("validBuildNumber(%d) = true, want false", n)
		}
	}
}
