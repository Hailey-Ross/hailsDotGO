package masterfile

import (
	"encoding/json"
	"slices"
	"sort"
	"testing"
)

// Shapes copied from the live masterfile on 2026-10-27, trimmed to the fields CostumeForms reads.
const fixture = `{
  "costumes": {"1": {"name": "Anniversary", "proto": "ANNIVERSARY"}},
  "pokemon": {
    "41":  {"name": "Zubat", "forms": {
      "0":    {"name": "Unset", "proto": "FORM_UNSET"},
      "157":  {"name": "Normal", "proto": "ZUBAT_NORMAL"},
      "3362": {"name": "Halloween 2026 01", "proto": "ZUBAT_HALLOWEEN_2026_01", "isCostume": true}}},
    "122": {"name": "Mr. Mime", "forms": {
      "0": {"name": "Unset", "proto": "FORM_UNSET"},
      "1": {"name": "Normal", "proto": "MR_MIME_NORMAL"},
      "2": {"name": "Galarian", "proto": "MR_MIME_GALARIAN"},
      "3": {"name": "Party 2030", "proto": "MR_MIME_PARTY_2030", "isCostume": true}}},
    "854": {"name": "Sinistea", "forms": {
      "0": {"name": "Unset", "proto": "FORM_UNSET"},
      "1": {"name": "Phony", "proto": "SINISTEA_PHONY"},
      "2": {"name": "P Halloween 2026 01", "proto": "SINISTEA_P_HALLOWEEN_2026_01", "isCostume": true}}}
  }
}`

func TestCostumeFormsSpellsCodesLikeTheAssetTree(t *testing.T) {
	var d Data
	if err := json.Unmarshal([]byte(fixture), &d); err != nil {
		t.Fatal(err)
	}
	got := d.CostumeForms()
	sort.Slice(got, func(i, j int) bool { return got[i].Dex < got[j].Dex })

	want := []CostumeForm{
		{Dex: 41, Code: "HALLOWEEN_2026_01", Name: "Halloween 2026 01"},
		{Dex: 122, Code: "PARTY_2030", Name: "Party 2030"},                   // prefix read off MR_MIME_NORMAL
		{Dex: 854, Code: "P_HALLOWEEN_2026_01", Name: "P Halloween 2026 01"}, // no NORMAL form: name fallback
	}
	if !slices.Equal(got, want) {
		t.Errorf("CostumeForms() =\n  %+v\nwant\n  %+v", got, want)
	}
	// Unflagged forms (Galarian) never appear: only upstream's own flag admits without art.
	for _, f := range got {
		if f.Code == "GALARIAN" {
			t.Error("a regional form is not a costume")
		}
	}
}

func TestProtoName(t *testing.T) {
	for in, want := range map[string]string{"Mr. Mime": "MR_MIME", "Zubat": "ZUBAT", "Ho-Oh": "HO_OH", "Type: Null": "TYPE_NULL"} {
		if got := protoName(in); got != want {
			t.Errorf("protoName(%q) = %q, want %q", in, got, want)
		}
	}
}
