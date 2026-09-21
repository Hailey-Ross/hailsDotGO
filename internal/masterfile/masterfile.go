// Package masterfile reads WatWowMap's generated game masterfile, which is where the pretty
// costume names and the isCostume flag come from.
//
// It lives in its own package because two callers need the same answer to the same question and
// must not disagree: cmd/synccostumes, when it derives the catalog, and internal/costumes, when
// the admin panel asks whether upstream has a costume we have not synced yet. When the drift
// check reimplemented that rule itself it got it wrong, and reported "nothing new" for two years
// of costumes.
//
// Deliberately NOT part of internal/costumes: that package go:embeds catalog.json, so the tool
// that regenerates catalog.json would stop building whenever that file was mid-write or invalid.
package masterfile

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// URL is the generated masterfile. Unpinned on purpose, unlike the asset CDN: this file supplies
// names and flags only, never art, so a bad fetch degrades a label rather than breaking a sprite.
const URL = "https://raw.githubusercontent.com/WatWowMap/Masterfile-Generator/master/master-latest-everything.json"

type Data struct {
	Costumes map[string]struct {
		Name  string `json:"name"`
		Proto string `json:"proto"`
	} `json:"costumes"`
	Pokemon map[string]struct {
		Name  string `json:"name"`
		Forms map[string]struct {
			Name      string `json:"name"`
			Proto     string `json:"proto"`
			IsCostume bool   `json:"isCostume"`

			// Battle identity. A form carries these when it changes what the Pokemon IS in a
			// fight, which is what separates a regional, mega or battle form from a costume: a
			// costume changes the picture and nothing else. Held as raw JSON because the signal
			// is presence, not value, and a zero struct cannot tell the two apart.
			//
			// Move pools are deliberately NOT here. Plenty of real costumes carry an event move
			// pool (every GO Tour Pikachu does), so treating moves as battle identity would
			// reject the very codes this is meant to rescue. Neither are evolutions or
			// tempEvolutions: CHARIZARD_NORMAL carries tempEvolutions too.
			Types       json.RawMessage `json:"types"`
			Stats       json.RawMessage `json:"stats"`
			FormChanges json.RawMessage `json:"formChanges"`
			GmaxMove    json.RawMessage `json:"gmaxMove"`
		} `json:"forms"`
	} `json:"pokemon"`
}

// Form is what the masterfile knows about one .f code on one species.
//
// IsCostumeForm collapses all of this into a single bool, which throws away the case this type
// exists for: a form upstream KNOWS about but has not flagged. That is not a rare edge. It is how
// Clone Pikachu, Psyduck's swim ring and Friede's Goggles all went missing, the last of them for
// months, with shiny art sitting upstream the whole time.
type Form struct {
	Name string // the masterfile's display name, e.g. "Goggles 2026"

	// Found is true when the masterfile has this form for this dex at all. False means the code
	// came from the asset tree and upstream has no record of it, so nothing here can judge it.
	Found bool

	// Flagged is upstream's own isCostume. When true the answer is settled and no other signal
	// is consulted.
	Flagged bool

	// Battle is true when the form overrides battle identity, which a costume never does. This is
	// a veto, not evidence: it is what keeps pm26.fALOLA and pm888.fCROWNED_SWORD out.
	//
	// Measured against the whole masterfile: of 98 flagged costume forms exactly 2 trip this,
	// Armored Mewtwo and Galarian Corsola's spring costume (which inherits its regional typing).
	// Both are already flagged, so the veto never has to judge them.
	Battle bool
}

// Costume reports whether this form can be admitted on upstream's word alone.
func (f Form) Costume() bool { return f.Flagged }

// Plausible reports whether an UNFLAGGED form is shaped like a costume: upstream knows it, and it
// changes nothing about battle identity.
//
// This is necessary but NOT sufficient, and must never be used on its own. Ordinary cosmetic forms
// pass it too, because a Vivillon pattern and an Unown letter are also just a different picture.
// Corroboration from a second source is what separates those from a costume.
func (f Form) Plausible() bool { return f.Found && !f.Flagged && !f.Battle }

// Lookup returns everything the masterfile knows about a .f code on a dex.
//
// Matching follows IsCostumeForm's rule for the same reason: species names resist normalisation
// (Mr. Mime, Farfetch'd, Nidoran-F), so match the form proto against the code the asset tree
// produced for that same dex instead.
func (d *Data) Lookup(dex int, code string) Form {
	pk, ok := d.Pokemon[strconv.Itoa(dex)]
	if !ok {
		return Form{}
	}
	for _, form := range pk.Forms {
		if form.Proto != code && !strings.HasSuffix(form.Proto, "_"+code) {
			continue
		}
		return Form{
			Name:    form.Name,
			Found:   true,
			Flagged: form.IsCostume,
			Battle: form.Types != nil || form.Stats != nil ||
				form.FormChanges != nil || form.GmaxMove != nil,
		}
	}
	return Form{}
}

// Load fetches and parses the masterfile. client may be nil, in which case a 60 second one is used.
func Load(client *http.Client) (*Data, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Get(URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s -> %d: %s", URL, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var d Data
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	if len(d.Costumes) == 0 || len(d.Pokemon) == 0 {
		return nil, fmt.Errorf("masterfile is missing costumes or pokemon")
	}
	return &d, nil
}

// IsCostumeForm reports whether a .f code on this dex is a COSTUME rather than an ordinary
// alternate form, and returns its display name.
//
// This filter is mandatory. The .f prefix is shared with regional, mega, battle and cosmetic
// forms, so without it the catalog would happily ingest pm26.fALOLA (Alolan Raichu) and
// pm888.fCROWNED_SWORD as costumes.
//
// Rather than normalise species names to strip the proto prefix (Mr. Mime, Farfetch'd and
// Nidoran-F all break naive casing rules), match the form proto against the code the asset
// tree produced for that same dex: PIKACHU_VISOR_2026 ends with _VISOR_2026.
// It answers only the settled question. For the unflagged case, which is where costumes go
// missing, use Lookup.
func (d *Data) IsCostumeForm(dex int, code string) (string, bool) {
	f := d.Lookup(dex, code)
	if !f.Costume() {
		return "", false
	}
	return f.Name, true
}

// CostumeName is the display name for a .c costume overlay code, which lives in the costumes
// enum rather than among the per-species forms. Takes the bare proto, without the "c:" prefix.
func (d *Data) CostumeName(proto string) string {
	for _, c := range d.Costumes {
		if c.Proto == proto {
			return c.Name
		}
	}
	return ""
}

// NameToDex maps english species name to dex number.
func (d *Data) NameToDex() map[string]int {
	out := make(map[string]int, len(d.Pokemon))
	for dexStr, pk := range d.Pokemon {
		if dex, err := strconv.Atoi(dexStr); err == nil && pk.Name != "" {
			out[pk.Name] = dex
		}
	}
	return out
}
