package costumes

// Finding costumes without being asked.
//
// The drift check next door already spots new codes, but it can only ever SAY something: it prints
// a sentence into a panel nobody is scheduled to open, and a restart forgets it. That is how
// f:GOGGLES_2026 stayed invisible for months while its art and its published name both sat
// upstream. This file closes the loop, by deciding.
//
// What it may decide is deliberately narrow. It admits codes to the runtime catalog, which makes
// their sprites resolvable; it never writes a label. A code with no label cannot be offered in the
// picker or typed by a trainer, so the worst an over-eager admission can do is put a row in a
// review queue. Naming stays a human decision because a label is user data that can never be
// renamed. Everything it cannot judge becomes a candidate, which is a question, not an answer.

import (
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"pogo.hails.cc/internal/costumenames"
	"pogo.hails.cc/internal/masterfile"
)

// formLookup is the masterfile's answer to what a .f code actually is. An interface so the whole
// decision table can be exercised without the network.
type formLookup interface {
	// Lookup takes the BARE code, without its "f:" prefix: the masterfile's protos are
	// SPECIES_CODE, so a prefixed lookup silently matches nothing and every form reads as unknown.
	Lookup(dex int, code string) masterfile.Form
	// CostumeName answers for "c:" codes, which live in the costumes enum instead of among the
	// per-species forms. Also bare.
	CostumeName(proto string) string
	NameToDex() map[string]int
}

// nameSource suggests what a costume is called. Injected for the same reason.
type nameSource interface {
	// Name returns the human name for a (dex, species, code), "" when there is no page for it.
	Name(dex int, species, code string) (string, error)
}

// Discovered is one thing a pass decided about.
type Discovered struct {
	Code      string `json:"code"`
	Dex       []int  `json:"dex"`
	Label     string `json:"label"` // the suggested name, when there is one
	Why       string `json:"why"`
	Candidate bool   `json:"candidate"`
}

// DiscoveryReport is what a pass changed. Only what it changed: a pass that finds nothing new
// reports nothing, which is what makes running it hourly cheap and quiet.
type DiscoveryReport struct {
	Admitted   []Discovered
	Candidates []Discovered
	Scanned    int
	Commit     string
	Notes      []string // non-fatal problems, e.g. the masterfile or Dittobase being unreachable
}

// Changed reports whether this pass decided anything at all.
func (r DiscoveryReport) Changed() bool { return len(r.Admitted)+len(r.Candidates) > 0 }

// maxNameLookups bounds how many Dittobase pages one pass will fetch.
//
// A big event drop can add a dozen codes at once, and a polite source is one that does not arrive
// as a burst. Whatever is left over is picked up next pass, and since names are cached forever the
// backlog drains rather than repeating.
const maxNameLookups = 12

// Seams for the tests.
var (
	loadMasterfile = func() (formLookup, error) {
		mf, err := masterfile.Load(&http.Client{Timeout: 30 * time.Second})
		if err != nil {
			return nil, err
		}
		return mf, nil
	}
	newNameSource = func(cachePath string) (nameSource, func(), error) {
		return newDittobase(cachePath)
	}
)

// Discover scans the upstream asset tree and acts on what it finds.
//
// namesCache is where suggested names are remembered between passes, so the steady state costs
// nothing upstream. Errors that only degrade the answer (the masterfile down, Dittobase down) are
// collected into Notes rather than returned: a pass that can still say "there is something new
// here, go and look" is far more useful than one that gives up.
func Discover(force bool, namesCache string) (DiscoveryReport, error) {
	rep := DiscoveryReport{}

	files, sha, _, err := assets(force)
	if err != nil {
		return rep, err
	}
	rep.Commit = sha

	// What upstream has, by code, with the species that have DEFAULT shiny art. Female-only art
	// never creates a code on its own, exactly as the catalog builder and the drift check agree.
	upstream := map[string][]int{}
	for _, f := range files {
		m := driftRe.FindStringSubmatch(f)
		if m == nil {
			continue
		}
		rep.Scanned++
		dex, code, female := parseDriftMatch(m)
		if code == "" || female {
			continue
		}
		if !slices.Contains(upstream[code], dex) {
			upstream[code] = append(upstream[code], dex)
		}
	}

	// Only codes nothing here has an opinion on yet. Everything else was judged on an earlier
	// pass, by a deploy, or by an admin, and re-judging it would re-alert forever.
	var fresh []string
	for code := range upstream {
		if !KnownCode(code) {
			fresh = append(fresh, code)
		}
	}
	if len(fresh) == 0 {
		return rep, nil
	}
	sort.Strings(fresh)

	var forms formLookup
	if mf, err := loadMasterfile(); err != nil {
		rep.Notes = append(rep.Notes, "the masterfile was unreachable, so unflagged codes could not be judged: "+err.Error())
	} else {
		forms = mf
	}

	// Dittobase is only consulted for codes that reach the corroboration step, and only then is
	// the sitemap fetched at all.
	var ditto nameSource
	var closeDitto func()
	lookups := 0

	curated := labelledCodes()
	dexToName := map[int]string{}
	if forms != nil {
		for name, dex := range forms.NameToDex() {
			dexToName[dex] = name
		}
	}

	for _, code := range fresh {
		dexes := slices.Clone(upstream[code])
		sort.Ints(dexes)

		verdict, why := classify(code, dexes, forms, curated)

		if verdict == verdictAsk {
			// The last question: has anyone out there given this thing a name? Ordinary alternate
			// forms never get one, so a real name is the corroboration that separates a costume
			// from a Vivillon pattern.
			if lookups < maxNameLookups {
				if ditto == nil && closeDitto == nil {
					d, done, err := newNameSource(namesCache)
					if err != nil {
						rep.Notes = append(rep.Notes, "dittobase was unreachable, so nothing could be corroborated this pass: "+err.Error())
						closeDitto = func() {} // do not retry for the rest of the pass
					} else {
						ditto, closeDitto = d, done
					}
				}
				if ditto != nil {
					lookups++
					if name, ok := corroborate(ditto, code, dexes, dexToName); ok {
						if err := Admit(code, dexes, sha, prettyOf(code, dexes, forms), name,
							SourceCorroborated, why+"; dittobase calls it "+strconv.Quote(name), ""); err != nil {
							rep.Notes = append(rep.Notes, "admit "+code+": "+err.Error())
							continue
						}
						rep.Admitted = append(rep.Admitted, Discovered{Code: code, Dex: dexes, Label: name,
							Why: why + "; dittobase calls it " + strconv.Quote(name)})
						continue
					}
				}
			}
			// Nothing corroborated it. That is NOT a verdict of "not a costume": an echo just
			// means nobody upstream has named it, which is equally true of a Vivillon pattern and
			// of a costume that dropped this morning. The year token is what decides whether it is
			// worth a human's attention, the same rule the drift check already uses to tell a
			// permanent form apart from an event costume.
			if !eventYearRe.MatchString(strings.TrimPrefix(code, "f:")) {
				continue
			}
			verdict = verdictCandidate
			why = "upstream does not flag this form a costume and nothing corroborates it, but the code is shaped like an event costume"
		}

		switch verdict {
		case verdictAdmit:
			if err := Admit(code, dexes, sha, prettyOf(code, dexes, forms), "", sourceOf(code, forms, curated), why, ""); err != nil {
				rep.Notes = append(rep.Notes, "admit "+code+": "+err.Error())
				continue
			}
			rep.Admitted = append(rep.Admitted, Discovered{Code: code, Dex: dexes, Why: why})
		case verdictCandidate:
			c := Candidate{Code: code, Dex: dexes, AssetBase: AssetBaseFor(sha), SourceCommit: sha,
				Pretty: prettyOf(code, dexes, forms), Why: why}
			if err := AddCandidate(c); err != nil {
				rep.Notes = append(rep.Notes, "record "+code+": "+err.Error())
				continue
			}
			rep.Candidates = append(rep.Candidates, Discovered{Code: code, Dex: dexes, Why: why, Candidate: true})
		}
	}
	if closeDitto != nil {
		closeDitto()
	}

	if rep.Changed() {
		log.Printf("costumes: discovery admitted %d, queued %d for review (assets @ %.12s)",
			len(rep.Admitted), len(rep.Candidates), sha)
	}
	return rep, nil
}

type verdict int

const (
	verdictIgnore    verdict = iota // an ordinary alternate form; say nothing
	verdictAdmit                    // upstream or our own labels vouch for it
	verdictAsk                      // shaped like a costume, but needs a second source
	verdictCandidate                // a human has to look at the sprite
)

// classify applies the decision table to one code. The ORDER is load bearing; see each step.
func classify(code string, dexes []int, forms formLookup, curated map[string]bool) (verdict, string) {
	// 1. A .c code is a costume overlay and nothing else. Unambiguous from the asset tree alone,
	//    which is why a brand new costume drop still works when the masterfile lags.
	if strings.HasPrefix(code, "c:") {
		return verdictAdmit, "a costume overlay code, which is unambiguous from the asset tree"
	}

	// 2. A label already vouching for it settles the matter, and this clause is not a loophole:
	//    isCostume has false negatives, so trusting the flag alone would drop a costume trainers
	//    may already have recorded.
	if curated[code] {
		return verdictAdmit, "a curated label already vouches for this code"
	}

	if forms == nil {
		// Nothing could be judged. Say so only when it looks like an event costume, or every
		// Vivillon pattern would land in the review queue the first time the masterfile is down.
		if eventYearRe.MatchString(strings.TrimPrefix(code, "f:")) {
			return verdictCandidate, "the masterfile was unreachable, so this could not be judged automatically"
		}
		return verdictIgnore, ""
	}

	flagged, found, battle := false, false, false
	bare := strings.TrimPrefix(code, "f:")
	for _, dex := range dexes {
		f := forms.Lookup(dex, bare)
		if f.Flagged {
			flagged = true
		}
		if f.Found {
			found = true
		}
		if f.Battle {
			battle = true
		}
	}

	// 3. Upstream's own flag, tested BEFORE the veto. Galarian Corsola's spring costume is a real
	//    costume that carries its regional typing, so the veto must never get to judge something
	//    upstream has already confirmed.
	if flagged {
		return verdictAdmit, "upstream flags this form a costume"
	}

	// 4. A code upstream has no record of cannot be judged on shape. If it looks like an event
	//    costume it is worth a human's time; otherwise it is almost certainly not one.
	if !found {
		if eventYearRe.MatchString(strings.TrimPrefix(code, "f:")) {
			return verdictCandidate, "upstream has no record of this form, but the code is shaped like an event costume"
		}
		return verdictIgnore, ""
	}

	// 5. The veto. A costume changes the picture and nothing else, so anything that overrides
	//    types, stats, form changes or a Gigantamax move is a regional, mega or battle form.
	if battle {
		return verdictIgnore, ""
	}

	// 6. Shaped like a costume, but so is every Vivillon pattern and Unown letter. Needs a second
	//    source before it can be admitted.
	return verdictAsk, "upstream knows this form but does not flag it a costume, and it changes nothing about battle identity"
}

// corroborate asks whether anyone has given this code a real name, which is the signal that
// separates a costume from an ordinary cosmetic form.
//
// Agreement from ANY species counts, because Dittobase names a shared code differently per species.
// An echo of the code is not corroboration: it means Dittobase has no name either, which is true
// both of a Vivillon pattern and of a genuinely new costume nobody has named yet.
func corroborate(src nameSource, code string, dexes []int, dexToName map[int]string) (string, bool) {
	for _, dex := range dexes {
		species := dexToName[dex]
		if species == "" {
			continue
		}
		name, err := src.Name(dex, species, code)
		if err != nil || name == "" {
			continue
		}
		if costumenames.IsEcho(name, species, code) {
			continue
		}
		return name, true
	}
	return "", false
}

func prettyOf(code string, dexes []int, forms formLookup) string {
	if forms == nil {
		return ""
	}
	if rest, ok := strings.CutPrefix(code, "c:"); ok {
		return forms.CostumeName(rest)
	}
	bare := strings.TrimPrefix(code, "f:")
	for _, dex := range dexes {
		if f := forms.Lookup(dex, bare); f.Name != "" {
			return f.Name
		}
	}
	return ""
}

func sourceOf(code string, forms formLookup, curated map[string]bool) string {
	switch {
	case strings.HasPrefix(code, "c:"):
		return SourceCostumePrefix
	case curated[code]:
		return SourceCurated
	default:
		return SourceMasterfile
	}
}

// parseDriftMatch pulls the species, code and female flag out of a driftRe match, so the discovery
// scan reads the filename grammar exactly as the drift check does rather than near enough.
func parseDriftMatch(m []string) (dex int, code string, female bool) {
	d, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}
	female = m[4] == ".g2"
	switch {
	case m[3] != "":
		return d, "c:" + m[3], female
	case m[2] != "":
		return d, "f:" + m[2], female
	}
	return d, "", female
}
