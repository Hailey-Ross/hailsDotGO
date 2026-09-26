package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"pogo.hails.cc/internal/costumes"
)

// unnamedCostume is one row of the admin review queue: a costume the game has that we have not
// given a label, and which therefore cannot be recorded by anyone.
type unnamedCostume struct {
	costumes.Unnamed
	Species []string        `json:"species"`
	Sprites []costumeSprite `json:"sprites"`
}

// costumeSprite is the costume on one species. A code usually covers several (c:FALL_2018 is
// Pikachu, Raichu and Togepi), and you are naming the costume rather than one Pokemon, so the admin
// tab shows the whole set at full size when a thumbnail is clicked.
type costumeSprite struct {
	Dex     int    `json:"dex"`
	Species string `json:"species"`
	URL     string `json:"url"`
}

// upcomingCostume is a costume that exists and is named but is not in the game yet. Species and
// Sprites are filled here rather than in the package, which knows dex numbers and not names.
type upcomingCostume struct {
	costumes.Upcoming
	Sprites []costumeSprite `json:"sprites"`
}

// AdminCostumes lists the costumes nobody has named yet, with the art for each, so an admin can
// see what they are naming. The code names are no help ("Gotour 2026 A"), and worse, they mislead:
// c:MAY_2019_NOEVOLVE is a straw hat, not the May 2019 Detective Pikachu tie-in. Four labels were
// wrong because someone reasoned from a code name instead of looking at the sprite.
func (h *Handlers) AdminCostumes(w http.ResponseWriter, r *http.Request) {
	names := make(map[int]string)
	for _, p := range h.store.PokemonList() {
		names[p.ID] = p.Name
	}

	list := costumes.Unlabelled()
	out := make([]unnamedCostume, 0, len(list))
	for _, c := range list {
		row := unnamedCostume{Unnamed: c}
		for _, dex := range c.Dex {
			name := names[dex]
			if name != "" {
				row.Species = append(row.Species, name)
			}
			// SpriteURLFor gates on the catalog, so anything it returns is a file the sprite proxy
			// will serve: the zoom cannot render a broken image.
			if url, ok := costumes.SpriteURLFor(dex, c.Code); ok {
				if name == "" {
					name = fmt.Sprintf("dex %d", dex)
				}
				row.Sprites = append(row.Sprites, costumeSprite{Dex: dex, Species: name, URL: url})
			}
		}
		out = append(out, row)
	}

	cands := costumes.Candidates()
	queue := make([]candidateCostume, 0, len(cands))
	for _, c := range cands {
		row := candidateCostume{Candidate: c}
		for _, dex := range c.Dex {
			name := names[dex]
			if name != "" {
				row.Species = append(row.Species, name)
			}
			if name == "" {
				name = fmt.Sprintf("dex %d", dex)
			}
			// Candidates are not in the catalog, so SpriteURLFor refuses them. The proxy serves
			// them anyway (AllowedFile admits discovered candidates), because judging a costume
			// means looking at it.
			row.Sprites = append(row.Sprites, costumeSprite{
				Dex: dex, Species: name, URL: costumes.SpritePath + costumes.CandidateFile(dex, c.Code),
			})
		}
		queue = append(queue, row)
	}

	// Upcoming rows carry the whole species strip, exactly as the backlog and the candidate queue
	// do. A held costume is the one most likely to be renamed upstream, and checking whether a
	// rename moved the artwork means looking at every species that wears it, not at whichever
	// happens to be first in the list.
	up := costumes.UpcomingCostumes()
	upcoming := make([]upcomingCostume, 0, len(up))
	for _, u := range up {
		row := upcomingCostume{Upcoming: u}
		for _, dex := range u.Dex {
			name := names[dex]
			if name != "" {
				row.Species = append(row.Species, name)
			}
			if url, ok := costumes.SpriteURLFor(dex, u.Code); ok {
				if name == "" {
					name = fmt.Sprintf("dex %d", dex)
				}
				row.Sprites = append(row.Sprites, costumeSprite{Dex: dex, Species: name, URL: url})
			}
		}
		upcoming = append(upcoming, row)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"costumes": out,
		// out, queue and upcoming are built with make, so they are lists even when empty. named
		// returns nil when this panel has named nothing, which is the ordinary state of a fresh
		// server rather than an edge case, so it goes through jsonList. Every list on this
		// endpoint is a list, always.
		"candidates": queue,
		"upcoming":   upcoming,
		"named":      jsonList(h.namedHere(names)),
		"counts": map[string]int{
			"pending":    len(out),
			"candidates": len(queue),
			"upcoming":   len(upcoming),
		},
	})
}

// AdminReleaseCostume marks a costume as being in the game now, for when it is live before
// upstream notices, or when upstream never says.
//
// It refuses a costume upstream has renamed since it was labelled. That is the one case where a
// human pressing the button is not enough: the label may now describe a different costume, and
// releasing it would put the wrong picture on everything recorded afterwards. Re-name it first.
func (h *Handlers) AdminReleaseCostume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		costumeErr(w, http.StatusBadRequest, "could not read the request")
		return
	}

	by := ""
	if u := h.currentUser(r); u != nil {
		by = u.Username
	}
	note, err := costumes.RecordCheck(body.Code, "", "", false, by)
	if err != nil {
		costumeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if note.Blocked {
		// The old wording said to name it again, which is the one thing that cannot be done: the
		// label is already set, naming is add-only and un-naming refuses once a trainer has used
		// it. What is actually needed is a human looking at the artwork, so say that instead and
		// point at the route that records the answer.
		costumeErr(w, http.StatusConflict, fmt.Sprintf(
			"upstream now calls this %q, not %q. Look at the sprite: if it is still the costume "+
				"we named, confirm the name and release it again; if the artwork has moved, the "+
				"label is wrong and cannot be renamed",
			note.NameChanged, note.Label))
		return
	}

	log.Printf("costumes: %s released %s (%s) by hand", by, body.Code, note.Label)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// AdminConfirmCostumeName says our label still describes the artwork after upstream renamed the
// costume, which is the only way out of the release block and the other half of that 409.
//
// It renames nothing. The label is untouched; what changes is the upstream name we compare
// against next time, so the guard keeps protecting the NEXT rename instead of latching forever.
func (h *Handlers) AdminConfirmCostumeName(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		costumeErr(w, http.StatusBadRequest, "could not read the request")
		return
	}

	by := ""
	if u := h.currentUser(r); u != nil {
		by = u.Username
	}
	label, was, err := costumes.ConfirmName(body.Code, by)
	if err != nil {
		costumeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("costumes: %s confirmed %s is still %q after upstream renamed it from %q",
		by, body.Code, label, was)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// candidateCostume is a code the discovery job could not judge: it has shiny art, but nothing
// upstream vouches for it and nothing corroborates it. Shown with its sprite, because the sprite
// is the one thing that reliably settles the question and the code name actively misleads.
type candidateCostume struct {
	costumes.Candidate
	Species []string        `json:"species"`
	Sprites []costumeSprite `json:"sprites"`
}

// AdminAdmitCostume records an admin's verdict that a candidate really is a costume. It enters the
// catalog and becomes resolvable at once, but stays unnamed: naming is a separate, deliberate act,
// because a label is user data that can never be renamed.
func (h *Handlers) AdminAdmitCostume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		costumeErr(w, http.StatusBadRequest, "could not read the request")
		return
	}

	by := ""
	if u := h.currentUser(r); u != nil {
		by = u.Username
	}
	if err := costumes.AdmitCandidate(body.Code, by); err != nil {
		costumeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("costumes: %s admitted %s from the review queue", by, body.Code)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// AdminDismissCostume records the opposite verdict, permanently. Without it the same ordinary
// alternate forms would be re-offered every hour, and a review queue nobody can empty is a review
// queue nobody reads.
func (h *Handlers) AdminDismissCostume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		costumeErr(w, http.StatusBadRequest, "could not read the request")
		return
	}

	by := ""
	if u := h.currentUser(r); u != nil {
		by = u.Username
	}
	if err := costumes.Dismiss(body.Code, by); err != nil {
		costumeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("costumes: %s dismissed %s as not a costume", by, body.Code)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// AdminDiscoverCostumes runs a discovery pass now, rather than waiting for the hourly one. The
// answer is the pass itself, so an admin watching for an event drop can see what it decided.
func (h *Handlers) AdminDiscoverCostumes(w http.ResponseWriter, r *http.Request) {
	rep, err := costumes.Discover(r.URL.Query().Get("refresh") == "1", costumeNamesCache())
	if err != nil {
		costumeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	h.alertNewCostumes()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(discoverCostumesResponse(rep))
}

// discoverCostumesResponse is the body of a discovery pass, split out from the handler because
// the shape it has to get right belongs to the answer nobody looks at.
//
// Nothing new upstream is the normal, healthy result, and it is the one where a report that is
// only ever appended to has all three lists still nil. So the response that ships every day is
// exactly the one a hand-written sample payload never shows.
func discoverCostumesResponse(rep costumes.DiscoveryReport) map[string]any {
	return map[string]any{
		"ok":         true,
		"admitted":   jsonList(rep.Admitted),
		"candidates": jsonList(rep.Candidates),
		"scanned":    rep.Scanned,
		"notes":      jsonList(rep.Notes),
		"synced":     rep.Commit,
	}
}

// namedCostume is one costume this panel named, with what it would cost to take the name back.
type namedCostume struct {
	costumes.NamedHere
	Species []string `json:"species"`
	Used    int      `json:"used"` // trainer entries recorded under this label
}

// namedHere lists the names an admin can still take back, and how many trainers have already used
// each one.
//
// The count is the whole point of showing this. Removing a label that entries already reference
// would blank the costume art on them, so the handler refuses; saying so up front is kinder than a
// button that fails when pressed. A count that cannot be read is reported as in use, because
// guessing "nobody" and being wrong is the expensive direction.
func (h *Handlers) namedHere(species map[int]string) []namedCostume {
	list := costumes.NamedInPanel()
	if len(list) == 0 {
		return nil
	}

	used := map[string]int{}
	counted := true
	rows, err := h.db.Query(`SELECT costume, COUNT(*) FROM user_shinies WHERE costume <> '' GROUP BY costume`)
	if err != nil {
		log.Printf("costumes: count label use: %v", err)
		counted = false
	} else {
		defer rows.Close()
		for rows.Next() {
			var label string
			var n int
			if err := rows.Scan(&label, &n); err != nil {
				log.Printf("costumes: scan label use: %v", err)
				counted = false
				break
			}
			used[label] = n
		}
		if err := rows.Err(); err != nil {
			log.Printf("costumes: count label use: %v", err)
			counted = false
		}
	}
	if !counted {
		// Unknown, so assume in use. The endpoint checks again and refuses anyway; offering a
		// button that is going to fail is the worse of the two ways to be wrong here.
		for _, n := range list {
			used[n.Label] = 1
		}
	}

	out := make([]namedCostume, 0, len(list))
	for _, n := range list {
		row := namedCostume{NamedHere: n, Used: used[n.Label]}
		for _, dex := range n.Dex {
			if name := species[dex]; name != "" {
				row.Species = append(row.Species, name)
			}
		}
		out = append(out, row)
	}
	return out
}

// AdminCheckCostumes asks the mined-asset repo whether the game has costumes we have not synced.
//
// It cannot apply what it finds. The catalog is compiled into the binary, so picking up a new
// costume means running `make costumes` and deploying. Saying so plainly is the point: the button
// tells you there is work to do, it does not pretend to have done it.
//
// ?refresh=1 bypasses the cached upstream listing. The tab sends it on a second press, so the
// first press is cheap and an admin watching for an event drop is never more than one more click
// away from a live answer. The 5/min per IP limit on the route is what bounds it.
func (h *Handlers) AdminCheckCostumes(w http.ResponseWriter, r *http.Request) {
	// Choose the check before running it. Running the cached one and then discarding its answer for
	// the fresh one would spend the five upstream API calls the cache exists to save, twice over.
	check := costumes.DriftCheck
	if r.URL.Query().Get("refresh") == "1" {
		check = costumes.DriftCheckFresh
	}
	res := check()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":     res.Error == "",
		"result": res,
		// The asset commit the embedded catalog was built from, so an admin can see how old the
		// costume data is without going to the repo.
		"synced": costumes.SourceCommit(),
	})
}

// AdminNameCostume gives an unnamed costume a label. It is live in the picker at once: the label
// goes into an overlay file merged over the embedded labels.json, exactly as approved translations
// are layered over the embedded locales.
//
// It can only ever ADD. costumes.Name refuses a code that already has a label, because a label is
// user data (user_shinies.costume is free text trainers type) and renaming one orphans the costume
// art on entries people have already saved.
func (h *Handlers) AdminNameCostume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		costumeErr(w, http.StatusBadRequest, "could not read the request")
		return
	}

	by := ""
	if u := h.currentUser(r); u != nil {
		by = u.Username
	}
	if err := costumes.Name(body.Code, body.Label, by); err != nil {
		costumeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("costumes: %s named %s %q", by, body.Code, body.Label)
	triggerCostumeSync()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// AdminHideCostume drops a code out of the review queue: the game flags some things as costumes
// that no trainer would record (the Gimmighoul coins).
func (h *Handlers) AdminHideCostume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		costumeErr(w, http.StatusBadRequest, "could not read the request")
		return
	}
	if err := costumes.Hide(body.Code); err != nil {
		costumeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	triggerCostumeSync()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// AdminUnnameCostume undoes a naming mistake, and ONLY while nothing depends on it.
//
// A label cannot be renamed once trainers have recorded it, so this is the escape hatch for a typo.
// The guard is what makes it safe: if any user_shinies row already carries this label, removing it
// would blank the costume art on that entry, so refuse. Orphaning becomes impossible by
// construction rather than by remembering not to do it.
func (h *Handlers) AdminUnnameCostume(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	label := costumes.LabelOf(code)
	if label == "" {
		costumeErr(w, http.StatusBadRequest, "that costume was not named here, so it cannot be unnamed")
		return
	}

	var used int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM user_shinies WHERE costume = ?`, label).Scan(&used); err != nil {
		log.Printf("costumes: count users of %q: %v", label, err)
		costumeErr(w, http.StatusInternalServerError, "could not check whether the label is in use")
		return
	}
	if used > 0 {
		costumeErr(w, http.StatusConflict, fmt.Sprintf(
			"%d trainer entr%s already use %q; removing it would blank the costume art on them",
			used, plural(used, "y", "ies"), label))
		return
	}

	if err := costumes.Unname(code); err != nil {
		costumeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	triggerCostumeSync()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func costumeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
