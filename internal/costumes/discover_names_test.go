package costumes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pogo.hails.cc/internal/costumenames"
)

// The steady state must cost nothing upstream, and that is not a comment, it is a promise the
// wiki and the mobile handoff both repeat.
//
// It matters more than it looks. A code that is neither admitted nor queued is re-judged on every
// pass, deliberately, so the naming step is reached hourly forever by ordinary alternate forms:
// in production the Spinda spot patterns do exactly that. The first version of this fetched the
// sitemap when the source was constructed, which turned "zero requests" into one an hour, for
// nothing. Nothing in the flow would have noticed.
func TestCachedAnswersMakeNoRequests(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "costume_names.json")

	seed := nameCache{
		Names:  costumenames.Names{},
		Misses: map[string]string{"f:00|327": time.Now().UTC().Format(time.RFC3339)},
	}
	seed.Names.Set("f:02", 327, "Spinda 02")
	b, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	src, done, err := newDittobase(path)
	if err != nil {
		t.Fatalf("newDittobase: %v", err)
	}
	defer done()

	// Constructing it must not have reached the network.
	d := src.(*dittoSource)
	if d.fetched {
		t.Error("the sitemap was fetched before anything needed it")
	}

	if got, err := src.Page(327, "Spinda", "f:02"); err != nil || got.Name != "Spinda 02" {
		t.Errorf("cached name = %q, %v; want Spinda 02", got.Name, err)
	}
	if got, err := src.Page(327, "Spinda", "f:00"); err != nil || got.Name != "" {
		t.Errorf("cached miss = %q, %v; want an empty answer", got.Name, err)
	}
	if d.fetched {
		t.Error("a cached hit and a cached miss both went to the network")
	}
}

// A miss is remembered for a week, not forever: a costume can get a page days after its art is
// mined, and caching that answer permanently would strand it in the review queue.
func TestAStaleMissIsRetried(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "costume_names.json")

	old := time.Now().Add(-missTTL - time.Hour).UTC().Format(time.RFC3339)
	b, _ := json.Marshal(nameCache{Names: costumenames.Names{}, Misses: map[string]string{"f:X|1": old}})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	src, done, _ := newDittobase(path)
	defer done()
	d := src.(*dittoSource)

	// Force the resolver to a known failure rather than reaching dittobase from a test.
	d.fetched, d.resErr = true, os.ErrDeadlineExceeded

	if _, err := src.Page(1, "Bulbasaur", "f:X"); err == nil {
		t.Error("an expired miss should be looked up again, not answered from the cache")
	}
}
