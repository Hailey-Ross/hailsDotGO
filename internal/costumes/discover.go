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

// nameSource suggests what a costume is called, and says whether the game has it yet. Injected
// for the same reason.
type nameSource interface {
	// Page returns what upstream says about a (dex, species, code). A zero Page with a nil error
	// means there is no page for it, which is different from the source being unreachable.
	Page(dex int, species, code string) (costumenames.Page, error)
}

// Discovered is one thing a pass decided about.
type Discovered struct {
	Code      string `json:"code"`
	Dex       []int  `json:"dex"`
	Label     string `json:"label"` // the suggested name, when there is one
	Why       string `json:"why"`
	Candidate bool   `json:"candidate"`

	// Upcoming means the art exists but the game does not have it yet, so it is shown and not
	// recordable. ReleaseDate is "" when upstream has not said when.
	Upcoming    bool   `json:"upcoming,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`
}

// DiscoveryReport is what a pass changed. Only what it changed: a pass that finds nothing new
// reports nothing, which is what makes running it hourly cheap and quiet.
type DiscoveryReport struct {
	Admitted   []Discovered
	Candidates []Discovered
	// Released is what a re-check of waiting costumes concluded this pass: ones that went live,
	// and ones held back because upstream renamed them.
	Released []ReleaseNote
	// Held is what this pass newly discovered is not in the game yet.
	Held    []Discovered
	Scanned int
	Commit  string
	Notes   []string // non-fatal problems, e.g. the masterfile or Dittobase being unreachable
}

// Changed reports whether this pass decided anything at all.
func (r DiscoveryReport) Changed() bool {
	return len(r.Admitted)+len(r.Candidates)+len(r.Released)+len(r.Held) > 0
}

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
	sort.Strings(fresh)

	// NOT an early return on "nothing new upstream". Discovering codes is only one of this pass's
	// three jobs: it must also notice that a costume already here is not in the game yet, and
	// re-ask about the ones that are waiting. Nothing new IS the steady state, so returning here
	// silently disabled the entire releasing half, and it would have looked healthy forever,
	// because a quiet pass and a skipped pass are indistinguishable from outside.
	if len(fresh) == 0 && len(WaitingCodes()) == 0 && len(Unlabelled()) == 0 {
		return rep, nil
	}

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

	// Fetched on first genuine need and shared by every loop below, so a pass that only has to
	// re-check a release does not pay for a sitemap it will not use, and one that does pays once.
	ensureDitto := func() nameSource {
		if ditto != nil || closeDitto != nil {
			return ditto
		}
		d, done, err := newNameSource(namesCache)
		if err != nil {
			rep.Notes = append(rep.Notes, "dittobase was unreachable this pass: "+err.Error())
			closeDitto = func() {} // do not retry for the rest of the pass
			return nil
		}
		ditto, closeDitto = d, done
		return ditto
	}

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
				if ensureDitto() != nil {
					lookups++
					if page, ok := corroborate(ditto, code, dexes, dexToName); ok {
						name := page.Name
						if err := Admit(code, dexes, sha, prettyOf(code, dexes, forms), name,
							SourceCorroborated, why+"; dittobase calls it "+strconv.Quote(name), ""); err != nil {
							rep.Notes = append(rep.Notes, "admit "+code+": "+err.Error())
							continue
						}
						// Mined art routinely lands before the event that ships it. A costume
						// upstream does not yet call released is shown but held back, rather than
						// being offered for trainers to record something they cannot have.
						upcoming := !page.Released &&
							(page.ReleaseDate == "" || page.ReleaseDate > costumeNow())
						if upcoming {
							if err := HoldForRelease(code, name, page.ReleaseDate); err != nil {
								rep.Notes = append(rep.Notes, "hold "+code+": "+err.Error())
							}
						} else if page.HasReleased {
							// Settled, so say so: otherwise the check below re-asks about this
							// costume on every pass for as long as it goes unnamed.
							_ = NoteReleased(code, page.ReleaseDate)
						}
						rep.Admitted = append(rep.Admitted, Discovered{Code: code, Dex: dexes, Label: name,
							Upcoming: upcoming, ReleaseDate: page.ReleaseDate,
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
	// Costumes already in the catalog but not yet named are the ones an admin is about to make
	// recordable, so their release state matters now rather than later. There are only ever a
	// handful, and each is asked about once: a hold, once written, is re-checked by the loop
	// below instead.
	//
	// This exists because the first version only ever held back things it had just discovered,
	// and the one genuinely unreleased costume on the site had shipped in the embedded catalog
	// months earlier. It was invisible to exactly the mechanism meant to catch it.
	justAdmitted := map[string]bool{}
	for _, d := range rep.Admitted {
		justAdmitted[d.Code] = true
	}
	for _, u := range Unlabelled() {
		if len(u.Dex) == 0 || holdFor(u.Code) != nil || Dismissed(u.Code) || justAdmitted[u.Code] {
			continue
		}
		species := dexToName[u.Dex[0]]
		if species == "" || ensureDitto() == nil {
			continue
		}
		// Shares the pass's budget rather than having its own. Two unbounded loops against the
		// same host is the burst this limit exists to prevent, and the leftovers are picked up
		// next pass anyway.
		if lookups >= maxNameLookups {
			break
		}
		lookups++
		p, err := ditto.Page(u.Dex[0], species, u.Code)
		if err != nil || !p.HasReleased {
			continue // upstream has no opinion; the name cache stops this being re-asked often
		}
		if p.Released {
			// Record the answer rather than dropping it, or every pass asks again about every
			// costume awaiting a name, forever. This holds nothing back.
			_ = NoteReleased(u.Code, p.ReleaseDate)
			continue
		}
		name := p.Name
		if costumenames.IsEcho(name, species, u.Code) {
			name = "" // no real name to verify against later
		}
		if err := HoldForRelease(u.Code, name, p.ReleaseDate); err != nil {
			rep.Notes = append(rep.Notes, "hold "+u.Code+": "+err.Error())
			continue
		}
		rep.Held = append(rep.Held, Discovered{Code: u.Code, Dex: u.Dex, Label: name,
			Upcoming: true, ReleaseDate: p.ReleaseDate,
			Why: "upstream says this is not in the game yet"})
	}

	// Ask again about anything that is waiting on a release. Done here, inside the same pass, so
	// it shares the one Dittobase session and the same politeness budget.
	if len(WaitingCodes()) > 0 {
		if ditto == nil && closeDitto == nil {
			if d, done, err := newNameSource(namesCache); err == nil {
				ditto, closeDitto = d, done
			} else {
				rep.Notes = append(rep.Notes, "could not re-check waiting costumes: "+err.Error())
			}
		}
		if ditto != nil {
			rep.Released = recheckWaiting(ditto, dexToName)
		}
	}

	if closeDitto != nil {
		closeDitto()
	}

	for _, h := range rep.Held {
		log.Printf("costumes: %s is not in the game yet; it will be shown as coming soon%s",
			h.Code, dateSuffix(h.ReleaseDate))
	}
	for _, n := range rep.Released {
		if n.Blocked {
			log.Printf("costumes: %s was NOT released: upstream now calls it %q, not %q",
				n.Code, n.NameChanged, n.Label)
		} else {
			log.Printf("costumes: %s (%s) is in the game now and is recordable", n.Code, n.Label)
		}
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
func corroborate(src nameSource, code string, dexes []int, dexToName map[int]string) (costumenames.Page, bool) {
	for _, dex := range dexes {
		species := dexToName[dex]
		if species == "" {
			continue
		}
		p, err := src.Page(dex, species, code)
		if err != nil || p.Name == "" {
			continue
		}
		if costumenames.IsEcho(p.Name, species, code) {
			continue
		}
		return p, true
	}
	return costumenames.Page{}, false
}

// recheckWaiting asks upstream again about every costume that is not in the game yet.
//
// This is the half that makes "coming soon" resolve itself. It re-reads the name as well as the
// release state, because a costume renamed upstream after an admin labelled it must NOT go live
// on its own: the label could now describe something else entirely.
func recheckWaiting(src nameSource, dexToName map[int]string) []ReleaseNote {
	var notes []ReleaseNote
	for _, code := range WaitingCodes() {
		_, c := view()
		entry, ok := c.codes[code]
		if !ok || len(entry.Dex) == 0 {
			continue
		}
		species := dexToName[entry.Dex[0]]
		if species == "" {
			continue
		}
		p, err := src.Page(entry.Dex[0], species, code)
		if err != nil {
			continue // unreachable is not news; try again next pass
		}
		note, err := RecordCheck(code, p.Name, p.ReleaseDate, p.Released, "")
		if err != nil {
			continue
		}
		if note.Released || note.Blocked {
			notes = append(notes, note)
		}
	}
	return notes
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

// dateSuffix reads as a fact either way: most costumes never get an announced day.
func dateSuffix(d string) string {
	if d == "" {
		return " with no announced date"
	}
	return " until " + d
}
