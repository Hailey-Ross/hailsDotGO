package pogodata

import "testing"

// The origin allowlist for event page scraping. It used to be
// strings.HasSuffix(u.Host, "leekduck.com"), which is not a domain check at all:
// "evilleekduck.com" ends in "leekduck.com" and passed straight through, turning
// this into an attacker-directed fetch.
//
// Only refusal cases are asserted here. A permitted host would make a real network
// request, and the check runs before that, so refusal is the whole surface worth
// testing.
func TestScrapeEventPageRefusesForeignHosts(t *testing.T) {
	s := &Store{}

	refused := []string{
		"https://evilleekduck.com/events/x",   // the suffix bypass
		"https://leekduck.com.evil.test/x",    // domain as a prefix of another
		"https://notleekduck.com/x",           //
		"http://leekduck.com/x",               // scheme must be https
		"https://evil.test/x",                 // unrelated host
		"https://leekduck.com@evil.test/x",    // userinfo trick: real host is evil.test
		"file:///etc/passwd",                  // not http at all
		"https://127.0.0.1/x",                 // loopback
		"https://169.254.169.254/latest/meta", // cloud metadata endpoint
		"://malformed",                        // unparseable
	}
	for _, u := range refused {
		if _, err := s.scrapeEventPage(u); err == nil {
			t.Errorf("scrapeEventPage(%q) was allowed, want refusal", u)
		}
	}
}
