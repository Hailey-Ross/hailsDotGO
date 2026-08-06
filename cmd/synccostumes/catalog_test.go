package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// committedCatalog is the real file, from the repo root two levels up.
const committedCatalog = "../../internal/costumes/catalog.json"

func writeTemp(t *testing.T, cat *catalog) string {
	t.Helper()
	b, err := marshalCatalog(cat)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func sample() *catalog {
	return &catalog{
		AssetBase:    fmt.Sprintf(cdnBase, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		SourceCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Codes: map[string]*catalogEntry{
			"c:FALL_2018": {Pretty: "Fall 2018", Suggested: "Witch Hat", Dex: []int{25, 26}, Female: []int{25}},
			"f:COPY_2019": {Pretty: "Copy 2019", Dex: []int{25}, Unconfirmed: true},
		},
	}
}

// The measured case this whole split exists for. On 2026-08-05 upstream HEAD was 1a4ad1fc and the
// committed catalog was pinned to 6280ccb8, both sides derived the same 140 codes, and
// `make costumes-check` still exited 1. PokeMiners commits several times a day, so that gate was
// red permanently while nothing about costumes had moved: exactly the alarm fatigue the -check
// branch's own comment was written to prevent.
func TestCatalogDiffIgnoresThePin(t *testing.T) {
	path := writeTemp(t, sample())

	next := sample()
	next.SourceCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	next.AssetBase = fmt.Sprintf(cdnBase, next.SourceCommit)

	d, err := catalogDiff(path, next)
	if err != nil {
		t.Fatalf("catalogDiff: %v", err)
	}
	if d.data {
		t.Error("data drift reported, want none: only the asset pin moved")
	}
	if !d.pin {
		t.Error("pin churn not reported, want it flagged so the run can say so")
	}
	if d.committedSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("committedSHA = %q, want the pin the file carries", d.committedSHA)
	}
}

// One case per field, so nobody later "simplifies" the comparison down to the set of codes and
// silently stops noticing a costume gaining a species.
func TestCatalogDiffSeesEveryDataField(t *testing.T) {
	path := writeTemp(t, sample())

	cases := []struct {
		what string
		edit func(*catalog)
	}{
		{"a species added to an existing code", func(c *catalog) {
			c.Codes["c:FALL_2018"].Dex = []int{25, 26, 172}
		}},
		{"a species removed", func(c *catalog) {
			c.Codes["c:FALL_2018"].Dex = []int{25}
		}},
		{"a new code", func(c *catalog) {
			c.Codes["c:BRAND_NEW"] = &catalogEntry{Pretty: "Brand New", Dex: []int{25}}
		}},
		{"a code disappearing", func(c *catalog) {
			delete(c.Codes, "f:COPY_2019")
		}},
		{"a changed suggested name", func(c *catalog) {
			c.Codes["c:FALL_2018"].Suggested = "Spooky Festival"
		}},
		{"a changed masterfile name", func(c *catalog) {
			c.Codes["c:FALL_2018"].Pretty = "Fall 2018 Noevolve"
		}},
		{"a changed female list", func(c *catalog) {
			c.Codes["c:FALL_2018"].Female = []int{25, 26}
		}},
		{"an unconfirmed flag flipping", func(c *catalog) {
			c.Codes["f:COPY_2019"].Unconfirmed = false
		}},
	}

	for _, tc := range cases {
		next := sample()
		tc.edit(next)
		d, err := catalogDiff(path, next)
		if err != nil {
			t.Fatalf("%s: catalogDiff: %v", tc.what, err)
		}
		if !d.data {
			t.Errorf("%s: reported no data drift, want drift", tc.what)
		}
	}
}

func TestCatalogDiffTreatsAMissingFileAsDataDrift(t *testing.T) {
	d, err := catalogDiff(filepath.Join(t.TempDir(), "nope.json"), sample())
	if err != nil {
		t.Fatalf("catalogDiff: %v", err)
	}
	if !d.data {
		t.Error("a missing catalog is not drift, want it written")
	}
}

// `make costumes` has to stay the repair for a corrupt catalog rather than becoming another thing
// that needs one. A UTF-8 BOM in this go:embed'ed file crashes the service on boot, and refusing to
// rewrite it would leave a dev with nothing to do but delete the file by hand.
func TestCatalogDiffRewritesAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	body, err := marshalCatalog(sample())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, body...), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	d, err := catalogDiff(path, sample())
	if err != nil {
		t.Fatalf("catalogDiff refused a BOM'd file: %v", err)
	}
	if !d.data {
		t.Error("a BOM'd catalog was reported as up to date; it must be rewritten")
	}
}

// The pin makes sprite URLs immutable, so an older pin is fine. A changed URL SHAPE is not: it
// means cdnBase moved (upstream renamed the folder, which has happened once already) and every
// sprite on the site is pointing at the wrong place until the file is written. That must not sit
// behind -repin.
func TestCatalogDiffTreatsAChangedURLShapeAsData(t *testing.T) {
	cur := sample()
	cur.AssetBase = "https://cdn.jsdelivr.net/gh/PokeMiners/pogo_assets@" + cur.SourceCommit + "/Images/Pokemon/Addressable%20Assets/"
	path := writeTemp(t, cur)

	d, err := catalogDiff(path, sample())
	if err != nil {
		t.Fatalf("catalogDiff: %v", err)
	}
	if !d.data {
		t.Error("an assetBase the committed pin does not imply was treated as pin churn, want data drift")
	}
}

// catalogDiff compares parsed structs rather than bytes, which is only sound while the committed
// file is exactly what marshalling produces. It also guards the BOM trap: a byte order mark, or
// PowerShell's CRLF, would fail here rather than at service boot.
func TestCommittedCatalogRoundTrips(t *testing.T) {
	raw, err := os.ReadFile(committedCatalog)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("catalog.json starts with a UTF-8 BOM; a go:embed'ed JSON file with one crashes the service on boot")
	}
	if bytes.Contains(raw, []byte("\r\n")) {
		t.Fatal("catalog.json has CRLF endings; it is written with LF and must stay that way")
	}

	var cur catalog
	if err := json.Unmarshal(raw, &cur); err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := marshalCatalog(&cur)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(raw, out) {
		t.Errorf("catalog.json does not round trip: %d bytes in, %d out. catalogDiff compares parsed structs, which only works while this holds.",
			len(raw), len(out))
	}
}
