package handlers

import "strconv"

// costumeAssetsBase mirrors POGO_ASSETS in ts/shared/costumes.ts. Costume art lives on
// the pokemon-go-api assets host, not the PokeAPI sprite host used for plain shinies.
const costumeAssetsBase = "https://raw.githubusercontent.com/pokemon-go-api/assets/main/Pokemon/"

// costumeSpriteURL resolves a shiny costume sprite for a species+costume the same way the
// client's costumeShinyUrl does: a species-specific override wins, else a shared costume
// the species is eligible for. Returns false when the costume does not resolve.
func costumeSpriteURL(dex int, pokemonName, costume string) (string, bool) {
	if costume == "" || dex == 0 {
		return "", false
	}
	if byLabel, ok := costumeOverrides[pokemonName]; ok {
		if c, ok := byLabel[costume]; ok {
			return costumeURL(dex, c.p, c.code), true
		}
	}
	for _, s := range sharedCostumes {
		if s.pending {
			continue
		}
		if s.label == costume && s.dex[dex] {
			return costumeURL(dex, s.p, s.code), true
		}
	}
	return "", false
}

func costumeURL(dex int, p, code string) string {
	return costumeAssetsBase + "pm" + strconv.Itoa(dex) + "." + p + code + ".s.icon.png"
}
