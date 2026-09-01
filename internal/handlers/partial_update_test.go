package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// The award and feedback-option writes used to decode into plain fields, so Go
// turned an absent key into the zero value and the handler wrote every column
// anyway. A body carrying only {"active": false} blanked the name and wiped the
// description. These pin the decode half of the fix: absent must stay nil, and
// an explicitly sent empty or false value must NOT.
func TestAwardUpdateDecodeDistinguishesAbsentFromCleared(t *testing.T) {
	var body struct {
		Name         *string `json:"name"`
		Description  *string `json:"description"`
		Icon         *string `json:"icon"`
		Color        *string `json:"color"`
		Active       *bool   `json:"active"`
		SortOrder    *int    `json:"sort_order"`
		MinGrantRank *int    `json:"min_grant_rank"`
	}
	if err := json.NewDecoder(strings.NewReader(`{"active": false}`)).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Active == nil || *body.Active {
		t.Error("active was sent as false and must survive as a real false")
	}
	for name, got := range map[string]any{
		"name": body.Name, "description": body.Description, "icon": body.Icon,
		"color": body.Color, "sort_order": body.SortOrder, "min_grant_rank": body.MinGrantRank,
	} {
		switch v := got.(type) {
		case *string:
			if v != nil {
				t.Errorf("%s was absent from the body but decoded to %q, so it would be written", name, *v)
			}
		case *int:
			if v != nil {
				t.Errorf("%s was absent from the body but decoded to %d, so it would be written", name, *v)
			}
		}
	}
}

// Clearing a field on purpose has to stay possible: an empty string sent
// explicitly is a real value, not an absence.
func TestAwardUpdateDecodeKeepsAnExplicitClear(t *testing.T) {
	var body struct {
		Description *string `json:"description"`
		Name        *string `json:"name"`
	}
	if err := json.NewDecoder(strings.NewReader(`{"description": ""}`)).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Description == nil {
		t.Fatal("an explicitly sent empty description must decode to a non-nil pointer")
	}
	if *body.Description != "" {
		t.Errorf("description = %q, want empty", *body.Description)
	}
	if body.Name != nil {
		t.Error("name was absent and must stay nil")
	}
}

func TestFeedbackOptionDecodeDistinguishesAbsentFromCleared(t *testing.T) {
	var body struct {
		Label     *string `json:"label"`
		Sentiment *string `json:"sentiment"`
		SortOrder *int    `json:"sort_order"`
		Enabled   *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(strings.NewReader(`{"enabled": false}`)).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Enabled == nil || *body.Enabled {
		t.Error("enabled was sent as false and must survive")
	}
	if body.Label != nil {
		t.Errorf("label was absent but decoded to %q, so disabling an option would blank its label", *body.Label)
	}
	if body.Sentiment != nil || body.SortOrder != nil {
		t.Error("absent fields must stay nil")
	}
}
