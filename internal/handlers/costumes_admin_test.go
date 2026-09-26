package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pogo.hails.cc/internal/costumes"
)

// TestDiscoverResponseSendsEmptyLists pins the shape of the answer a discovery pass gives on the
// day nothing happens, which is almost every day.
//
// DiscoveryReport is only ever appended to, so "nothing new upstream" leaves all three lists nil
// and Go marshals nil as null. A client that binds them as plain lists then works against every
// sample payload ever written by hand and throws on the response it actually receives.
func TestDiscoverResponseSendsEmptyLists(t *testing.T) {
	body, err := json.Marshal(discoverCostumesResponse(costumes.DiscoveryReport{Scanned: 1834}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)

	for _, want := range []string{`"admitted":[]`, `"candidates":[]`, `"notes":[]`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "null") {
		t.Errorf("null in a response that has no nullable field: %s", got)
	}
}

// TestDiscoverResponseKeepsWhatThePassFound is the other direction: normalizing empties must not
// flatten a pass that decided something.
func TestDiscoverResponseKeepsWhatThePassFound(t *testing.T) {
	rep := costumes.DiscoveryReport{
		Admitted: []costumes.Discovered{{Code: "f:GOGGLES_2026", Label: "Friede's Goggles"}},
		Notes:    []string{"dittobase unreachable"},
		Scanned:  1834,
		Commit:   "d4432a333e123bfe8f44153f51bf58aa8d34d5b1",
	}
	body, err := json.Marshal(discoverCostumesResponse(rep))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out struct {
		Admitted   []costumes.Discovered `json:"admitted"`
		Candidates []costumes.Discovered `json:"candidates"`
		Notes      []string              `json:"notes"`
		Scanned    int                   `json:"scanned"`
		Synced     string                `json:"synced"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Admitted) != 1 || out.Admitted[0].Code != "f:GOGGLES_2026" {
		t.Errorf("admitted = %+v", out.Admitted)
	}
	if len(out.Candidates) != 0 {
		t.Errorf("candidates = %+v, want empty", out.Candidates)
	}
	if len(out.Notes) != 1 || out.Notes[0] != "dittobase unreachable" {
		t.Errorf("notes = %v", out.Notes)
	}
	if out.Scanned != 1834 || out.Synced != rep.Commit {
		t.Errorf("scanned/synced = %d/%s", out.Scanned, out.Synced)
	}
}

// TestAdminCostumesSendsEmptyLists is the same promise one endpoint over.
//
// named and upcoming are the two lists here that a Go function returns as nil rather than empty,
// and "nothing named through this panel yet" plus "nothing waiting for release" is the ordinary
// state of a fresh server, not an edge case. Both must still be arrays.
//
// No database is needed and none is given: namedHere returns before it queries when the panel has
// named nothing, which is exactly the case under test.
func TestAdminCostumesSendsEmptyLists(t *testing.T) {
	costumes.Init(t.TempDir())
	h := &Handlers{store: costumeStore(t)}

	w := httptest.NewRecorder()
	h.AdminCostumes(w, httptest.NewRequest(http.MethodGet, "/api/mobile/v1/admin/costumes", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200", w.Code)
	}

	var out map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"costumes", "candidates", "named", "upcoming"} {
		raw, ok := out[field]
		if !ok {
			t.Errorf("%s missing entirely", field)
			continue
		}
		if string(raw) == "null" {
			t.Errorf("%s is null, want a list", field)
		}
	}
	if string(out["counts"]) == "null" {
		t.Error("counts is null, want an object")
	}
}

// TestJSONListLeavesAValueAlone covers the helper itself, including the case that makes it worth
// having: a slice that is empty but not nil is already correct and must not be reallocated into
// a different empty slice on every response.
func TestJSONListLeavesAValueAlone(t *testing.T) {
	var nilSlice []string
	if got := jsonList(nilSlice); got == nil || len(got) != 0 {
		t.Errorf("jsonList(nil) = %#v, want empty non-nil", got)
	}

	full := []string{"a"}
	if got := jsonList(full); len(got) != 1 || got[0] != "a" {
		t.Errorf("jsonList(%v) = %v", full, got)
	}
}
