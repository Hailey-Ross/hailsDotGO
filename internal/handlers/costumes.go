package handlers

import "pogo.hails.cc/internal/costumes"

// costumeSpriteURL resolves a shiny costume sprite for a species+costume. The data and the
// resolution rules live in internal/costumes, shared with the client (both read the same two
// JSON files), so the picker and the public profile can never disagree.
func costumeSpriteURL(dex int, pokemonName, costume string) (string, bool) {
	return costumes.SpriteURL(dex, pokemonName, costume)
}
