package handlers

import (
	"encoding/json"
	"net/http"

	"pogo.hails.cc/internal/costumes"
)

// costumeSpriteURL resolves a shiny costume sprite for a species+costume. The data and the
// resolution rules live in internal/costumes, shared with the client (both read the same two
// JSON files), so the picker and the public profile can never disagree.
func costumeSpriteURL(dex int, pokemonName, costume string) (string, bool) {
	return costumes.SpriteURL(dex, pokemonName, costume)
}

// mobileCostume is one costume a species can wear, precomputed. The sprite is FULLY RESOLVED on
// purpose: the mined asset filename form (pm25.cANNIVERSARY.s.icon.png) is a private detail of the
// asset tree and it has changed before, so no client should ever rebuild it.
type mobileCostume struct {
	Label   string   `json:"label"`
	Sprite  string   `json:"sprite"`
	Aliases []string `json:"aliases"`
}

// MobileCostumes serves the costume picker's contents precomputed per species, so the app needs no
// resolver of its own.
//
// The catalog is compiled into the TypeScript bundle and go:embed'ed here, but until now it was
// reachable only by a page: /api/costume-sprite/ serves images, the admin endpoint serves the
// unlabelled review queue, and the merged label set is injected into two templates as a script
// global. So the app shipped a free-text costume field, which is the same failure the web picker
// already fixed when a trainer reported the Willow costume as missing because a datalist did not
// render on their phone.
//
// Every rule stays in internal/costumes: LabelsForDex handles alias resolution, override beats
// shared, and the catalog gate, so a Kotlin resolver would only be a third mirror of a table that
// has no parity check. This deliberately does NOT serve LabelsJSON, which is labels.json byte for
// byte including its hand-written _comment blocks: that is a file-sync artifact, not an API shape.
//
// Only species with at least one costume are emitted, so this is a few hundred rows rather than the
// whole dex. The app treats the endpoint as optional and reads a 404 as "this server is too old",
// which is why an empty species map is never a valid response here.
func (h *Handlers) MobileCostumes(w http.ResponseWriter, r *http.Request) {
	// Keyed by species NAME, not dex: curated overrides live under l.Species[name], so a
	// dex-keyed response would lose the override lookup for every form-sharing species.
	species := make(map[string][]mobileCostume)

	for _, p := range h.store.PokemonList() {
		labelList := costumes.LabelsForDex(p.ID, p.Name)
		if len(labelList) == 0 {
			continue
		}
		rows := make([]mobileCostume, 0, len(labelList))
		for _, label := range labelList {
			// LabelsForDex only offers labels that resolve, so this cannot miss; gating on it
			// anyway means a row we emit is always one the sprite proxy will actually serve.
			sprite, ok := costumes.SpriteURL(p.ID, p.Name, label)
			if !ok {
				continue
			}
			aliases := costumes.AliasesFor(label)
			if aliases == nil {
				aliases = []string{} // never null: the app treats this field as always present
			}
			rows = append(rows, mobileCostume{Label: label, Sprite: sprite, Aliases: aliases})
		}
		if len(rows) == 0 {
			continue
		}
		species[p.Name] = rows
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		// A prefix hint for logging and cache keys, not something to concatenate: "sprite" above
		// is already complete.
		"sprite_base": costumes.SpritePath,
		"species":     species,
	})
}
