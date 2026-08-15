package eligibility

import (
	"encoding/json"
	"testing"
)

func mkRune(form, slot, host string, needsReview bool, req []Clause) RuneInfo {
	return RuneInfo{Form: form, Slot: slot, Host: host, NeedsReview: needsReview, Requires: req}
}

// Keen: damage_type in [piercing, slashing] AND weapon_type in [Melee] (a grouped
// AND of ORs). A property weapon rune.
var keen = mkRune("property", "property", "weapon", false, []Clause{
	{Op: "in", Path: "$.stat_block.offense.weapon_modes[*].damage[*].damage_type", Values: []string{"piercing", "slashing"}},
	{Op: "in", Path: "$.stat_block.offense.weapon_modes[*].weapon_type", Values: []string{"Melee"}},
})

func TestFactsFor(t *testing.T) {
	attrs := json.RawMessage(`{"weapon_types":["Melee"],"damage_types":["piercing"],
		"weapon_category":"Martial","traits":["Finesse"],"item_subcategory":"Base Weapons"}`)
	f, err := FactsFor("weapons", "Rapier", attrs)
	if err != nil {
		t.Fatal(err)
	}
	if f.Host != "weapon" || f.Category != "Martial" || f.Name != "Rapier" {
		t.Fatalf("facts wrong: %+v", f)
	}
	// Armor category falls back to armor_category when weapon_category is absent.
	af, _ := FactsFor("armor", "Plate", json.RawMessage(`{"armor_category":"Heavy"}`))
	if af.Host != "armor" || af.Category != "Heavy" {
		t.Fatalf("armor facts wrong: %+v", af)
	}
}

func TestRuneEligible_Clauses(t *testing.T) {
	meleePiercing := ItemFacts{Host: "weapon", WeaponTypes: []string{"Melee"}, DamageTypes: []string{"piercing"}}
	if !RuneEligible(keen, meleePiercing) {
		t.Error("Keen should fit a melee piercing weapon")
	}
	// Ranged fails the weapon_type clause.
	if RuneEligible(keen, ItemFacts{Host: "weapon", WeaponTypes: []string{"Ranged"}, DamageTypes: []string{"piercing"}}) {
		t.Error("Keen should not fit a ranged weapon")
	}
	// Bludgeoning-only fails the damage_type clause (even though melee).
	if RuneEligible(keen, ItemFacts{Host: "weapon", WeaponTypes: []string{"Melee"}, DamageTypes: []string{"bludgeoning"}}) {
		t.Error("Keen should not fit a bludgeoning weapon")
	}
	// Host mismatch.
	if RuneEligible(keen, ItemFacts{Host: "armor"}) {
		t.Error("a weapon rune should not fit armor")
	}
}

func TestRuneEligible_Rules(t *testing.T) {
	// An unconstrained fundamental potency rune (no clauses) fits any weapon…
	potency := mkRune("fundamental", "weapon_potency", "weapon", false, nil)
	if !RuneEligible(potency, ItemFacts{Host: "weapon"}) {
		t.Error("potency should fit a plain weapon")
	}
	// …but needs_review runes are excluded outright.
	nr := mkRune("property", "property", "weapon", true, nil)
	if RuneEligible(nr, ItemFacts{Host: "weapon"}) {
		t.Error("needs_review rune must be excluded")
	}
	// Shields take only the reinforcing slot.
	reinforcing := mkRune("fundamental", "reinforcing", "shield", false, nil)
	shieldProperty := mkRune("property", "property", "shield", false, nil)
	if !RuneEligible(reinforcing, ItemFacts{Host: "shield"}) {
		t.Error("reinforcing should fit a shield")
	}
	if RuneEligible(shieldProperty, ItemFacts{Host: "shield"}) {
		t.Error("a shield takes no property runes")
	}
	// Specific magic items + staves take no property runes.
	if RuneEligible(keen, ItemFacts{Host: "weapon", WeaponTypes: []string{"Melee"}, DamageTypes: []string{"piercing"},
		ItemSubcategory: "Specific Magic Weapons"}) {
		t.Error("a specific magic weapon takes no property runes")
	}
	if RuneEligible(keen, ItemFacts{Host: "weapon", WeaponTypes: []string{"Melee"}, DamageTypes: []string{"piercing"},
		ItemCategory: "Staves"}) {
		t.Error("a staff takes no property runes")
	}
}

func TestBuildRunes_Grouping(t *testing.T) {
	f := ItemFacts{Host: "weapon", WeaponTypes: []string{"Melee"}, DamageTypes: []string{"piercing"}}
	cands := []Candidate{
		{GameID: "r1", Name: "Weapon Potency", Attrs: mustAttrs(mkRune("fundamental", "weapon_potency", "weapon", false, nil))},
		{GameID: "r2", Name: "Keen", Attrs: mustAttrs(keen)},
		{GameID: "r3", Name: "Shifting", Attrs: mustAttrs(mkRune("property", "property", "weapon", false, []Clause{
			{Op: "in", Path: "$.stat_block.offense.weapon_modes[*].weapon_type", Values: []string{"Ranged"}}}))}, // ineligible (Ranged)
		{GameID: "bad", Name: "Junk", Attrs: json.RawMessage(`not json`)}, // skipped
	}
	g := BuildRunes(cands, f)
	if len(g.Fundamental) != 1 || g.Fundamental[0].GameID != "r1" {
		t.Fatalf("fundamental = %+v", g.Fundamental)
	}
	if len(g.Property) != 1 || g.Property[0].Name != "Keen" {
		t.Fatalf("property = %+v", g.Property)
	}
}

func TestBuildMaterials(t *testing.T) {
	cands := []Candidate{
		{GameID: "m1", Name: "Dawnsilver", Attrs: json.RawMessage(`{"material_precious":true,
			"material_grades":[{"grade":"standard","max_rune_level":15},{"grade":"high"}],
			"material_grants_traits":["Rare"]}`)},
	}
	out := BuildMaterials(cands)
	if len(out) != 1 || !out[0].Precious || out[0].GrantsTraits[0] != "Rare" {
		t.Fatalf("materials = %+v", out)
	}
	if out[0].Grades[1].MaxRuneLevel != nil {
		t.Error("high grade max_rune_level should be nil (unbounded)")
	}
}

func TestSpellsFor(t *testing.T) {
	if SpellsFor(json.RawMessage(`{"weapon_types":["Melee"]}`)) != nil {
		t.Error("a weapon has no spell slots")
	}
	s := SpellsFor(json.RawMessage(`{"spell_holder":"wand","spell_max_rank":9,
		"spell_excluded_types":["cantrip"],"spell_has_constraint_text":true}`))
	if s == nil || s.Holder != "wand" || !s.HasConstraintText {
		t.Fatalf("spells = %+v", s)
	}
}

func mustAttrs(r RuneInfo) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"rune_form": r.Form, "rune_slot": r.Slot, "rune_host": r.Host,
		"rune_needs_review": r.NeedsReview, "rune_requires": r.Requires,
	})
	return b
}
