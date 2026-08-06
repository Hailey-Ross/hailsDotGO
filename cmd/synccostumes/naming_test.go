package main

import (
	"strings"
	"testing"
)

// The causes want different responses from a human, and the aggregate counters beside them cannot
// tell them apart: "their pokedex has no page for this costume" is a dead end that has to be named
// from the sprite, "two of our codes collide on one page" is a rule we chose on purpose, and "we
// never asked" just means re-run without -offline. Every costume in the review queue had a blank
// suggestion and nothing anywhere said which it was.
func TestNoNameReasonsDistinguishesTheCauses(t *testing.T) {
	cat := &catalog{Codes: map[string]*catalogEntry{
		"c:NO_PAGE":   {Dex: []int{25}},
		"c:AMBIGUOUS": {Dex: []int{25}},
		"c:NOT_ASKED": {Dex: []int{25}},
		"c:ALREADY":   {Dex: []int{25}}, // has a cached name, so nothing to explain
	}}
	nm := names{}
	nm.set("c:ALREADY", 25, "Witch Hat")

	got := noNameReasons(cat, nm, []nameOutcome{
		{code: "c:NO_PAGE", dex: 25, reason: `no page: tried "pikachu-no-page" exactly and as a prefix/suffix match`},
		{code: "c:AMBIGUOUS", dex: 25, reason: "ambiguous: c:AMBIGUOUS_NOEVOLVE matches the same page, so naming either would be a guess"},
		{code: "c:NOT_ASKED", dex: 25, reason: "not attempted (-offline); re-run without -offline to find out why"},
		{code: "c:ALREADY", dex: 25, reason: "no page: whatever"},
	})

	if len(got) != 3 {
		t.Fatalf("explained %d codes (%v), want only the three that still have no name", len(got), got)
	}
	for code, want := range map[string]string{
		"c:NO_PAGE":   "no page",
		"c:AMBIGUOUS": "ambiguous",
		"c:NOT_ASKED": "not attempted",
	} {
		if !strings.Contains(got[code], want) {
			t.Errorf("%s explained as %q, want it to say %q", code, got[code], want)
		}
	}
	if _, noisy := got["c:ALREADY"]; noisy {
		t.Error("c:ALREADY was explained; a costume that already has a suggestion needs no explanation")
	}
}

// A costume on several species can fail differently per species. Reporting only the first would be
// a quiet lie, so the line says the species disagree.
func TestNoNameReasonsFlagsMixedCauses(t *testing.T) {
	cat := &catalog{Codes: map[string]*catalogEntry{"c:MIXED": {Dex: []int{31, 34}}}}

	got := noNameReasons(cat, names{}, []nameOutcome{
		{code: "c:MIXED", dex: 31, reason: "no page: tried x"},
		{code: "c:MIXED", dex: 34, reason: "ambiguous: c:OTHER matches the same page"},
	})

	if !strings.Contains(got["c:MIXED"], "reasons differ") {
		t.Errorf("detail = %q, want it to say the species disagree", got["c:MIXED"])
	}
}

// A name on any one species is enough for suggestedLabel to have something to offer, so the report
// stays quiet.
func TestNoNameReasonsIgnoresACodeNamedOnOneSpecies(t *testing.T) {
	cat := &catalog{Codes: map[string]*catalogEntry{"c:PARTLY": {Dex: []int{31, 34}}}}
	nm := names{}
	nm.set("c:PARTLY", 34, "Crown")

	got := noNameReasons(cat, nm, []nameOutcome{{code: "c:PARTLY", dex: 31, reason: "no page: tried x"}})

	if len(got) != 0 {
		t.Errorf("got %+v, want nothing: the costume already has a suggestion", got)
	}
}

// The reason has to reach the line a human actually reads. A separate section listing the same
// codes a second time would double a report whose baseline is meant to be quiet.
func TestReviewCarriesTheReasonThereIsNoName(t *testing.T) {
	cat := &catalog{Codes: map[string]*catalogEntry{"c:UNNAMED": {Pretty: "Gotour 2026 A", Dex: []int{25}}}}
	noName := map[string]string{"c:UNNAMED": "no page: tried \"pikachu-gotour-2026-a\""}

	_, _, review := audit(cat, &labels{}, map[string]int{"Pikachu": 25}, names{}, noName)

	if len(review) != 1 {
		t.Fatalf("review = %+v, want the one unlabelled costume", review)
	}
	if !strings.Contains(review[0].detail, "no page") {
		t.Errorf("review detail = %q, want it to explain the blank suggestion", review[0].detail)
	}
}
