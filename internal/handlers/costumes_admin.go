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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "costumes": out, "named": h.namedHere(names)})
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
