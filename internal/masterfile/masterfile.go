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
		} `json:"forms"`
	} `json:"pokemon"`
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
func (d *Data) IsCostumeForm(dex int, code string) (string, bool) {
	pk, ok := d.Pokemon[strconv.Itoa(dex)]
	if !ok {
		return "", false
	}
	for _, form := range pk.Forms {
		if form.Proto != code && !strings.HasSuffix(form.Proto, "_"+code) {
			continue
		}
		if !form.IsCostume {
			return "", false
		}
		return form.Name, true
	}
	return "", false
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
