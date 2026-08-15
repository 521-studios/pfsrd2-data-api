package eligibility

import "encoding/json"

// Response is the "what can I apply to this item?" payload. Runes are grouped by
// form (fundamental potency/striking first, then property); materials are a flat
// list carrying their grade caps; spells is present only when the item is itself a
// spell holder (scroll/wand/staff).
type Response struct {
	Item      ItemRef             `json:"item"`
	Runes     RuneGroups          `json:"runes"`
	Materials []MaterialCandidate `json:"materials"`
	Spells    *SpellOptions       `json:"spells,omitempty"`
}

// ItemRef identifies the item the eligibility was computed for. Host is "" when the
// item is not a rune/material host (e.g. a consumable or a spell holder).
type ItemRef struct {
	GameID string `json:"game_id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Host   string `json:"host,omitempty"`
}

// RuneGroups splits the eligible runes by form (fundamental potency/striking/…
// first, then property). Both are always present (possibly empty) for a stable shape.
type RuneGroups struct {
	Fundamental []RuneCandidate `json:"fundamental"`
	Property    []RuneCandidate `json:"property"`
}

// RuneCandidate is one eligible rune with the grades needed to render + pick one.
type RuneCandidate struct {
	GameID string  `json:"game_id"`
	Name   string  `json:"name"`
	Slot   string  `json:"slot"`
	Grades []Grade `json:"grades"`
}

// MaterialCandidate is one material the item can be made of, with its grade caps
// (the consumer applies max_rune_level once a grade is chosen).
type MaterialCandidate struct {
	GameID       string          `json:"game_id"`
	Name         string          `json:"name"`
	Precious     bool            `json:"precious"`
	Grades       []MaterialGrade `json:"grades,omitempty"`
	GrantsTraits []string        `json:"grants_traits,omitempty"`
}

// MaterialGrade is one grade of a material; MaxRuneLevel absent = unbounded (high).
type MaterialGrade struct {
	Grade        string `json:"grade"`
	MaxItemLevel *int   `json:"max_item_level,omitempty"`
	MaxRuneLevel *int   `json:"max_rune_level,omitempty"` // absent = unbounded (high grade)
}

// SpellOptions describes how to fill a holder's spell slot — not the list of legal
// spells (that's a search against max_rank + excluded types), but the constraints.
type SpellOptions struct {
	Holder            string   `json:"holder"`
	MaxRank           any      `json:"max_rank,omitempty"` // int or the string "cantrip"
	ExcludedTypes     []string `json:"excluded_types,omitempty"`
	HasConstraintText bool     `json:"has_constraint_text,omitempty"`
}

// Candidate is the minimal shape the handler passes in: a game_id, name and the
// raw index attrs, for both rune and material candidates.
type Candidate struct {
	GameID string
	Name   string
	Attrs  json.RawMessage
}

// BuildRunes filters rune candidates to those eligible for the item and groups them
// by form. Malformed attrs are skipped, not fatal.
func BuildRunes(candidates []Candidate, f ItemFacts) RuneGroups {
	groups := RuneGroups{Fundamental: []RuneCandidate{}, Property: []RuneCandidate{}}
	for _, c := range candidates {
		var r RuneInfo
		if err := json.Unmarshal(c.Attrs, &r); err != nil {
			continue
		}
		if !RuneEligible(r, f) {
			continue
		}
		rc := RuneCandidate{GameID: c.GameID, Name: c.Name, Slot: r.Slot, Grades: r.Grades}
		if r.Form == "property" {
			groups.Property = append(groups.Property, rc)
		} else {
			groups.Fundamental = append(groups.Fundamental, rc)
		}
	}
	return groups
}

// materialInfo is the subset of a material use-page's index attrs we surface.
type materialInfo struct {
	Precious     bool            `json:"material_precious"`
	Grades       []MaterialGrade `json:"material_grades"`
	GrantsTraits []string        `json:"material_grants_traits"`
}

// BuildMaterials turns material use-page candidates into the response list. The DB
// query already scoped them to the item's host, so no per-item filtering is needed;
// material grade caps are surfaced for the consumer to apply once a grade is chosen.
func BuildMaterials(candidates []Candidate) []MaterialCandidate {
	out := []MaterialCandidate{}
	for _, c := range candidates {
		var m materialInfo
		if err := json.Unmarshal(c.Attrs, &m); err != nil {
			continue
		}
		out = append(out, MaterialCandidate{
			GameID: c.GameID, Name: c.Name, Precious: m.Precious,
			Grades: m.Grades, GrantsTraits: m.GrantsTraits,
		})
	}
	return out
}

// SpellsFor returns the holder's spell-slot constraints when the item is itself a
// scroll/wand/staff, else nil.
func SpellsFor(attrs json.RawMessage) *SpellOptions {
	var s struct {
		Holder            string   `json:"spell_holder"`
		MaxRank           any      `json:"spell_max_rank"`
		ExcludedTypes     []string `json:"spell_excluded_types"`
		HasConstraintText bool     `json:"spell_has_constraint_text"`
	}
	if len(attrs) == 0 {
		return nil
	}
	if err := json.Unmarshal(attrs, &s); err != nil || s.Holder == "" {
		return nil
	}
	return &SpellOptions{
		Holder: s.Holder, MaxRank: s.MaxRank,
		ExcludedTypes: s.ExcludedTypes, HasConstraintText: s.HasConstraintText,
	}
}
