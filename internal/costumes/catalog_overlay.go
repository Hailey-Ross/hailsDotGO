package costumes

// A runtime catalog, merged over the embedded one.
//
// catalog.json is go:embed'ed, which means that before this file existed a costume the game had
// released could not become usable without a dev box running `make costumes` and a manual deploy.
// f:GOGGLES_2026 (Friede's Goggles) sat upstream with shiny art and a published name for months
// because of that, invisible to everyone: the admin panel could not even show it, since every
// lookup in this package gated on the embedded map.
//
// So the embedded catalog becomes a FLOOR rather than the whole truth, and an overlay under
// COSTUMES_DIR adds what the discovery job finds. Exactly the shape labels.json already uses, and
// for the same reason: the overlay is gitignored, is not in deploy.ps1's upload list, and deploy
// never deletes, so a discovery survives a deploy. Once a later `make costumes` folds the code
// into the embedded catalog, the overlay entry is redundant and is dropped on the next boot.
//
// Two files, not one. catalog.json is read on every sprite request and is backed up on every
// write; discovery.json is notification bookkeeping that churns whenever an alert is sent, and
// mixing them would rewrite and back up the resolvable catalog every time a push went out.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

// cdnBase mirrors cmd/synccostumes's template, and MUST stay identical to it.
//
// It cannot be imported: cmd/synccostumes regenerates catalog.json, and this package go:embeds
// that file, so depending on this package would stop the tool building whenever the file it is
// halfway through writing is invalid (the same reasoning as internal/masterfile's package doc).
// TestEmbeddedAssetBaseMatchesTheCDNTemplate pins the two copies together against the committed
// catalog, so a divergence fails on a dev box rather than serving 404 sprites in production.
const cdnBase = "https://cdn.jsdelivr.net/gh/PokeMiners/pogo_assets@%s/Images/Pokemon%%20-%%20256x256/Addressable%%20Assets/"

// AssetBaseFor builds the pinned CDN base for a mined-asset commit.
func AssetBaseFor(sha string) string { return fmt.Sprintf(cdnBase, sha) }

const overlayVersion = 1

// catalogOverlay is <COSTUMES_DIR>/catalog.json: codes the site may resolve that the embedded
// catalog does not carry.
type catalogOverlay struct {
	Version int                      `json:"version"`
	Codes   map[string]*catEntry `json:"codes"`
}

// catEntry pins its OWN asset commit, and that is not incidental.
//
// The embedded catalog pins one commit for every code it holds. A costume discovered later has
// art that exists only at a NEWER commit, so serving it off the embedded base would 404. Pinning
// per entry also means two discoveries a month apart keep their own bases, and a PokeMiners
// reorganization cannot retroactively re-point art we already serve. Keyed per entry rather than
// per file for the same reason.
type catEntry struct {
	AssetBase    string `json:"assetBase"`
	SourceCommit string `json:"sourceCommit"`
	Pretty       string `json:"pretty,omitempty"`    // the masterfile's machine name
	Suggested    string `json:"suggested,omitempty"` // what Dittobase calls it, if it knows
	Dex          []int  `json:"dex"`
	Source       string `json:"source"` // why it was admitted, see the Source* constants
	Why          string `json:"why"`    // the same thing in words, shown to the admin
	DiscoveredAt string `json:"discoveredAt"`
	By           string `json:"by,omitempty"` // set only when an admin admitted it by hand
}

// Why a code was admitted. Recorded so an admin reading the review queue can tell a costume
// upstream vouched for from one the site worked out for itself.
const (
	SourceCostumePrefix = "costume-prefix" // a c: code, unambiguous from the asset tree alone
	SourceMasterfile    = "masterfile"     // upstream flags the form a costume
	SourceCurated       = "curated"        // a label in labels.json already vouches for it
	SourceCorroborated  = "corroborated"   // unflagged, but shaped like a costume and named upstream
	SourceAdmin         = "admin"          // an admin looked at the sprite and said so
)

// Candidate is a code with shiny art that could NOT be judged automatically. It is not in the
// catalog, so nothing resolves it and no trainer can record it; it exists to be shown to a human
// with its sprite, which is the one thing that reliably settles the question.
type Candidate struct {
	Code         string `json:"code"`
	Pretty       string `json:"pretty,omitempty"`
	Suggested    string `json:"suggested,omitempty"`
	Dex          []int  `json:"dex"`
	AssetBase    string `json:"asset_base"`
	SourceCommit string `json:"source_commit"`
	Why          string `json:"why"`
	DiscoveredAt string `json:"discovered_at"`
	AlertedAt    string `json:"alerted_at,omitempty"`
	SpriteURL    string `json:"sprite_url,omitempty"` // filled on the way out, not stored
}

type dismissal struct {
	By string `json:"by"`
	At string `json:"at"`
}

// discoveryState is <COSTUMES_DIR>/discovery.json: what the job knows but the catalog does not.
type discoveryState struct {
	Version    int                   `json:"version"`
	Candidates map[string]*Candidate `json:"candidates"`
	Dismissed  map[string]dismissal  `json:"dismissed"`

	// Alerted stamps who has already been told about a code, so a job that runs hourly does not
	// tell them again every hour. It lives on disk rather than in memory because the drift check
	// already proved the in-memory version wrong: a restart forgets, and a service that restarts
	// on deploy would re-alert on every deploy.
	Alerted map[string]string `json:"alerted"`
}

// catView is the merged catalog every reader resolves against: the embedded floor plus whatever
// the overlay adds. Replaced wholesale under mu, never mutated, so a reader holding the pointer
// can use it without the lock.
type catView struct {
	codes map[string]*entry
	// base maps "code|dex" to the asset base that dex's art lives at, for overlay-supplied art
	// only. A miss means the embedded pin, which is the common case.
	base map[string]string
	// candidates are servable-but-not-resolvable: an admin may see the sprite, nothing else may.
	candidates map[string]*Candidate
}

func (c *catView) covers(code string, dex int) bool {
	e, ok := c.codes[code]
	return ok && slices.Contains(e.Dex, dex)
}

// showable reports whether a sprite may be proxied for this pair: resolvable codes, plus
// candidates, which an admin has to look at to judge.
func (c *catView) showable(code string, dex int) bool {
	if c.covers(code, dex) {
		return true
	}
	cand, ok := c.candidates[code]
	return ok && slices.Contains(cand.Dex, dex)
}

func baseKey(code string, dex int) string { return code + "|" + fmt.Sprint(dex) }

func catalogOverlayPath() string  { return filepath.Join(dir, "catalog.json") }
func discoveryOverlayPath() string { return filepath.Join(dir, "discovery.json") }

// loadCatalogOverlayLocked reads both runtime files and prunes what the embedded catalog has
// caught up with. Callers must hold mu.
//
// Called BEFORE the label overlay loads, and that order is load-bearing: the label loader drops
// labels whose code is not in the catalog, so running it first would throw away the label an
// admin gave a discovered costume, every single boot.
func loadCatalogOverlayLocked() {
	ovCat = catalogOverlay{Version: overlayVersion, Codes: map[string]*catEntry{}}
	disc = discoveryState{
		Version:    overlayVersion,
		Candidates: map[string]*Candidate{},
		Dismissed:  map[string]dismissal{},
		Alerted:    map[string]string{},
	}

	if data, err := os.ReadFile(catalogOverlayPath()); err == nil {
		var next catalogOverlay
		if err := json.Unmarshal(data, &next); err != nil {
			log.Printf("costumes: parse catalog overlay %s: %v (skipped)", catalogOverlayPath(), err)
		} else if next.Codes != nil {
			ovCat.Codes = next.Codes
		}
	} else if !os.IsNotExist(err) {
		log.Printf("costumes: read catalog overlay: %v", err)
	}

	// Superseded by a deploy: `make costumes` folded the code into the embedded catalog, so the
	// overlay copy is redundant. Dropping it matters beyond tidiness, because the overlay carries
	// its own asset pin and would otherwise keep winning for species the embedded catalog now
	// covers at its own, newer pin.
	for code, e := range ovCat.Codes {
		emb, ok := cat.Codes[code]
		if !ok {
			continue
		}
		e.Dex = slices.DeleteFunc(e.Dex, func(d int) bool { return slices.Contains(emb.Dex, d) })
		if len(e.Dex) == 0 {
			delete(ovCat.Codes, code)
			log.Printf("costumes: %s is in the embedded catalog now (overlay entry dropped)", code)
		}
	}

	if data, err := os.ReadFile(discoveryOverlayPath()); err == nil {
		var next discoveryState
		if err := json.Unmarshal(data, &next); err != nil {
			log.Printf("costumes: parse discovery state %s: %v (skipped)", discoveryOverlayPath(), err)
		} else {
			if next.Candidates != nil {
				disc.Candidates = next.Candidates
			}
			if next.Dismissed != nil {
				disc.Dismissed = next.Dismissed
			}
			if next.Alerted != nil {
				disc.Alerted = next.Alerted
			}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("costumes: read discovery state: %v", err)
	}

	// A candidate that has since been admitted, by an admin or by a later deploy, is answered.
	for code := range disc.Candidates {
		if _, ok := cat.Codes[code]; ok {
			delete(disc.Candidates, code)
			continue
		}
		if _, ok := ovCat.Codes[code]; ok {
			delete(disc.Candidates, code)
		}
	}

	if n := len(ovCat.Codes); n > 0 {
		log.Printf("costumes: %d code(s) from the catalog overlay", n)
	}
	if n := len(disc.Candidates); n > 0 {
		log.Printf("costumes: %d costume(s) awaiting a human, see the admin Costumes tab", n)
	}
}

// rebuildCatLocked recomputes the merged catalog. Callers must hold mu.
//
// The embedded catalog always wins on a collision: it is generated, reviewed and committed, while
// the overlay is a machine's guess. Embedded entry pointers are shared rather than copied because
// nothing ever mutates them after init; a code present in both gets a fresh entry so the union
// cannot write through to the embedded map.
func rebuildCatLocked() {
	next := &catView{
		codes:      make(map[string]*entry, len(cat.Codes)+len(ovCat.Codes)),
		base:       map[string]string{},
		candidates: make(map[string]*Candidate, len(disc.Candidates)),
	}
	for code, e := range cat.Codes {
		next.codes[code] = e
	}
	for code, oe := range ovCat.Codes {
		if len(oe.Dex) == 0 {
			continue
		}
		emb, ok := next.codes[code]
		if !ok {
			next.codes[code] = &entry{Pretty: oe.Pretty, Suggested: oe.Suggested, Dex: slices.Clone(oe.Dex)}
		} else {
			merged := &entry{Pretty: emb.Pretty, Suggested: emb.Suggested, Dex: slices.Clone(emb.Dex)}
			if merged.Pretty == "" {
				merged.Pretty = oe.Pretty
			}
			if merged.Suggested == "" {
				merged.Suggested = oe.Suggested
			}
			for _, d := range oe.Dex {
				if !slices.Contains(merged.Dex, d) {
					merged.Dex = append(merged.Dex, d)
				}
			}
			sort.Ints(merged.Dex)
			next.codes[code] = merged
		}
		// Only the dex the overlay actually supplies gets the overlay's pin. A species the
		// embedded catalog already covers keeps the commit its art was synced from.
		for _, d := range oe.Dex {
			if emb != nil && slices.Contains(emb.Dex, d) {
				continue
			}
			next.base[baseKey(code, d)] = oe.AssetBase
		}
	}
	for code, c := range disc.Candidates {
		next.candidates[code] = c
	}
	effCat = next
}

// writeJSONAtomic writes via a temp file and a rename, so a crash mid-write cannot leave a
// half-written file that the next boot would refuse to parse.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// commitCatalogLocked persists the catalog overlay and swaps the merged view in. Disk first, then
// memory: a failed write must leave the running site exactly as it was.
func commitCatalogLocked() error {
	if dir == "" {
		return fmt.Errorf("costumes: no overlay directory; Init was never called")
	}
	backupCatalogLocked()
	if err := writeJSONAtomic(catalogOverlayPath(), ovCat); err != nil {
		return err
	}
	rebuildCatLocked()
	return nil
}

// commitDiscoveryLocked persists the discovery bookkeeping. No backup: it holds no user data, and
// it is rewritten every time an alert goes out.
func commitDiscoveryLocked() error {
	if dir == "" {
		return fmt.Errorf("costumes: no overlay directory; Init was never called")
	}
	if err := writeJSONAtomic(discoveryOverlayPath(), disc); err != nil {
		return err
	}
	rebuildCatLocked()
	return nil
}

func backupCatalogLocked() {
	data, err := os.ReadFile(catalogOverlayPath())
	if err != nil {
		return // nothing to back up yet
	}
	bdir := filepath.Join(dir, "backup")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return
	}
	name := "catalog_" + time.Now().Format("20060102-150405") + ".json"
	if err := os.WriteFile(filepath.Join(bdir, name), data, 0o644); err != nil {
		log.Printf("costumes: back up catalog overlay: %v", err)
	}
}

// Admit adds a code to the runtime catalog, making it resolvable at once and with no deploy.
//
// It deliberately does NOT create a label. A code with no label is inert: it cannot be offered in
// the picker and no trainer can type it, so an admission that turns out to be wrong costs nothing
// but a row in the review queue. That is what makes auto-admission safe, and it is why naming
// stays a human decision: a label is user data that can never be renamed.
func Admit(code string, dex []int, sha, pretty, suggested, source, why, by string) error {
	if code == "" || len(dex) == 0 {
		return fmt.Errorf("costumes: admit %q: needs a code and at least one species", code)
	}

	mu.Lock()
	defer mu.Unlock()

	add := slices.Clone(dex)
	if emb, ok := cat.Codes[code]; ok {
		add = slices.DeleteFunc(add, func(d int) bool { return slices.Contains(emb.Dex, d) })
		if len(add) == 0 {
			return nil // the embedded catalog already covers every species asked for
		}
	}
	sort.Ints(add)

	if ovCat.Codes == nil {
		ovCat.Codes = map[string]*catEntry{}
	}
	if existing, ok := ovCat.Codes[code]; ok {
		for _, d := range add {
			if !slices.Contains(existing.Dex, d) {
				existing.Dex = append(existing.Dex, d)
			}
		}
		sort.Ints(existing.Dex)
	} else {
		ovCat.Codes[code] = &catEntry{
			AssetBase:    AssetBaseFor(sha),
			SourceCommit: sha,
			Pretty:       pretty,
			Suggested:    suggested,
			Dex:          add,
			Source:       source,
			Why:          why,
			DiscoveredAt: time.Now().UTC().Format(time.RFC3339),
			By:           by,
		}
	}
	// Admitting answers the question a candidate was asking.
	delete(disc.Candidates, code)

	if err := commitCatalogLocked(); err != nil {
		return err
	}
	return commitDiscoveryLocked()
}

// AddCandidate records a code that could not be judged, so an admin can look at the sprite. It is
// not admitted, so nothing trainer-facing can resolve it.
func AddCandidate(c Candidate) error {
	if c.Code == "" || len(c.Dex) == 0 {
		return fmt.Errorf("costumes: candidate %q: needs a code and at least one species", c.Code)
	}

	mu.Lock()
	defer mu.Unlock()

	if _, ok := disc.Dismissed[c.Code]; ok {
		return nil // an admin already said this is not a costume
	}
	if _, ok := disc.Candidates[c.Code]; ok {
		return nil // already waiting; do not reset its alert stamp
	}
	if disc.Candidates == nil {
		disc.Candidates = map[string]*Candidate{}
	}
	if c.DiscoveredAt == "" {
		c.DiscoveredAt = time.Now().UTC().Format(time.RFC3339)
	}
	c.SpriteURL = ""
	disc.Candidates[c.Code] = &c
	return commitDiscoveryLocked()
}

// Dismiss marks a candidate as not a costume, permanently. It is the escape hatch that keeps the
// review queue from filling with the same ordinary alternate forms every hour.
//
// It does not touch labels.json's hidden list, which Hide owns: Hide refuses a code the catalog
// does not have, and a candidate is by definition not in the catalog.
func Dismiss(code, by string) error {
	mu.Lock()
	defer mu.Unlock()

	if _, ok := disc.Candidates[code]; !ok {
		return fmt.Errorf("%s is not waiting for review", code)
	}
	delete(disc.Candidates, code)
	if disc.Dismissed == nil {
		disc.Dismissed = map[string]dismissal{}
	}
	disc.Dismissed[code] = dismissal{By: by, At: time.Now().UTC().Format(time.RFC3339)}
	return commitDiscoveryLocked()
}

// Candidates lists what is waiting for a human, sprite URLs filled in.
func Candidates() []Candidate {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]Candidate, 0, len(disc.Candidates))
	for _, c := range disc.Candidates {
		row := *c
		row.Dex = slices.Clone(c.Dex)
		if len(row.Dex) > 0 {
			row.SpriteURL = SpritePath + assetFile(row.Dex[0], row.Code)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// CandidateFile is the sprite filename for a candidate, which SpriteURLFor will not produce
// because a candidate is deliberately not in the catalog. Only the admin review tab uses it.
func CandidateFile(dex int, code string) string { return assetFile(dex, code) }

// AdmitCandidate is an admin saying "yes, that is a costume" about something the job could not
// judge. It enters the catalog with the admin's name on it and STILL has no label: deciding it is
// a costume and deciding what to call it are two different judgements, and only the second one
// creates user data that can never be undone.
func AdmitCandidate(code, by string) error {
	mu.RLock()
	c, ok := disc.Candidates[code]
	var dex []int
	var sha string
	var pretty, suggested string
	if ok {
		dex, sha, pretty, suggested = slices.Clone(c.Dex), c.SourceCommit, c.Pretty, c.Suggested
	}
	mu.RUnlock()

	if !ok {
		return fmt.Errorf("%s is not waiting for review", code)
	}
	return Admit(code, dex, sha, pretty, suggested, SourceAdmin,
		"an admin looked at the sprite and confirmed it is a costume", by)
}

// overlayEntryFor returns the runtime provenance for a code, or nil when the code came from the
// embedded catalog and there is nothing to say about it.
func overlayEntryFor(code string) *catEntry {
	mu.RLock()
	defer mu.RUnlock()
	return ovCat.Codes[code]
}

// Dismissed reports whether a code has been ruled out by hand.
func Dismissed(code string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := disc.Dismissed[code]
	return ok
}

// KnownCode reports whether anything here already knows about a code, so the discovery job can
// skip it without re-judging. Covers the embedded catalog, the overlay, candidates and dismissals.
func KnownCode(code string) bool {
	mu.RLock()
	defer mu.RUnlock()
	if _, ok := cat.Codes[code]; ok {
		return true
	}
	if _, ok := ovCat.Codes[code]; ok {
		return true
	}
	if _, ok := disc.Candidates[code]; ok {
		return true
	}
	_, ok := disc.Dismissed[code]
	return ok
}

// Alert is one thing an admin has not been told about yet.
type Alert struct {
	Code      string
	Label     string // the suggested name if there is one, else the code
	Dex       []int
	Candidate bool // true when it needs a human to judge, false when it just needs a name
}

// PendingAlerts lists discoveries nobody has been told about: admitted codes still waiting for a
// name, and candidates waiting for a verdict.
func PendingAlerts() []Alert {
	mu.RLock()
	defer mu.RUnlock()

	var out []Alert
	for code, e := range ovCat.Codes {
		if _, told := disc.Alerted[code]; told {
			continue
		}
		label := e.Suggested
		if label == "" {
			label = code
		}
		out = append(out, Alert{Code: code, Label: label, Dex: slices.Clone(e.Dex)})
	}
	for code, c := range disc.Candidates {
		if _, told := disc.Alerted[code]; told {
			continue
		}
		out = append(out, Alert{Code: code, Label: code, Dex: slices.Clone(c.Dex), Candidate: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// MarkAlerted records that admins have been told, so an hourly job does not tell them hourly.
//
// Called AFTER the notification is sent, not before. A crash in between re-alerts once on the next
// pass, which is the right direction to fail: a duplicate is an annoyance, a costume nobody hears
// about is the bug this feature exists to fix.
func MarkAlerted(codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()

	if disc.Alerted == nil {
		disc.Alerted = map[string]string{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range codes {
		disc.Alerted[c] = now
	}
	return commitDiscoveryLocked()
}

// ReviewCount is how many costumes are waiting on an admin: unnamed codes plus candidates.
//
// It runs on every page render for an admin, so it counts rather than building the lists. The
// obvious len(Unlabelled()) + len(Candidates()) allocates two slices and a sprite URL per row to
// throw them all away again.
func ReviewCount() int {
	l, c := view()

	labelled := map[string]bool{}
	for _, byLabel := range l.Species {
		for _, code := range byLabel {
			labelled[code] = true
		}
	}
	for _, s := range l.Shared {
		labelled[s.Code] = true
	}
	hidden := map[string]bool{}
	for _, h := range l.Hidden {
		hidden[h] = true
	}

	n := 0
	for code, e := range c.codes {
		if !labelled[code] && !hidden[code] && len(e.Dex) > 0 {
			n++
		}
	}
	return n + len(c.candidates)
}

// CatalogDelta is the overlay's codes in the embedded catalog's own shape, for injecting into the
// page so the browser resolver knows about a costume discovered since the last deploy.
//
// Without this the loop is only half closed: ts/shared/costumes.ts compiles catalog.json in and
// gates every lookup on it, so a discovered costume would resolve on public profiles and in the
// mobile app but stay untypeable in the shiny checklist picker until a deploy. Which is the bug,
// moved one step down the pipe.
func CatalogDelta() map[string]*entry {
	mu.RLock()
	defer mu.RUnlock()

	out := make(map[string]*entry, len(ovCat.Codes))
	for code, oe := range ovCat.Codes {
		if _, embedded := cat.Codes[code]; embedded {
			// The browser already has this one compiled in; only send what it is missing.
			continue
		}
		out[code] = &entry{Pretty: oe.Pretty, Suggested: oe.Suggested, Dex: slices.Clone(oe.Dex)}
	}
	return out
}
