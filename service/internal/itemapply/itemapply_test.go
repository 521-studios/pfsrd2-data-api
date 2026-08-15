package itemapply

import (
	"encoding/json"
	"testing"

	"github.com/521studios/pfsrd2-data-api/internal/eligibility"
	"github.com/521studios/pfsrd2-data-api/internal/template"
)

func TestKindOf(t *testing.T) {
	if KindOf("spells", nil) != KindSpell {
		t.Error("spells → KindSpell")
	}
	if KindOf("equipment", json.RawMessage(`{"rune_host":"weapon"}`)) != KindRune {
		t.Error("rune_host → KindRune")
	}
	if KindOf("equipment", json.RawMessage(`{"material_use_host":"weapon"}`)) != KindMaterial {
		t.Error("material → KindMaterial")
	}
	if KindOf("equipment", json.RawMessage(`{"item_category":"Consumables"}`)) != KindUnknown {
		t.Error("plain item → KindUnknown")
	}
}

func TestRuneVariantEffects_SelectsGradeAndNormalizesTarget(t *testing.T) {
	doc := map[string]any{"stat_block": map[string]any{"variants": []any{
		map[string]any{"level": float64(2), "name": "Weapon Potency (+1)", "effects": []any{
			map[string]any{"operation": "add_modifier",
				"target":   "$.stat_block.offense.weapon_modes[*].modifiers",
				"modifier": map[string]any{"type": "bonus", "subtype": "attack", "bonus_type": "item", "bonus_value": float64(1)}},
		}},
		map[string]any{"level": float64(10), "name": "Weapon Potency (+2)", "effects": []any{
			map[string]any{"operation": "add_modifier",
				"target":   "$.stat_block.offense.weapon_modes[*].modifiers",
				"modifier": map[string]any{"bonus_value": float64(2)}},
		}},
	}}}
	effs, label, err := RuneVariantEffects(doc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if label != "Weapon Potency (+2)" || len(effs) != 1 {
		t.Fatalf("label=%q effs=%d", label, len(effs))
	}
	// Target normalized from $.stat_block.… to the engine's stat_block-relative form.
	if effs[0].Target != "$.offense.weapon_modes[*].modifiers" {
		t.Fatalf("target = %q, want normalized", effs[0].Target)
	}
	// grade 0 → first (lowest) grade.
	_, label0, _ := RuneVariantEffects(doc, 0)
	if label0 != "Weapon Potency (+1)" {
		t.Fatalf("grade 0 label = %q, want +1", label0)
	}
}

func TestRuneVariantEffects_PropertyRuneNoEffects(t *testing.T) {
	// A property rune carries its mechanics as prose — the selected variant has no
	// executable effects. That's a 422 (handled), not a panic or empty apply.
	doc := map[string]any{"stat_block": map[string]any{"variants": []any{
		map[string]any{"level": float64(4), "name": "Flaming"}, // no "effects" key
	}}}
	if _, _, err := RuneVariantEffects(doc, 4); err == nil {
		t.Error("an effect-less property rune must error, not apply nothing")
	}
	// A rune doc with no stat_block at all also errors rather than panicking.
	if _, _, err := RuneVariantEffects(map[string]any{}, 0); err == nil {
		t.Error("a rune with no stat_block must error")
	}
}

func TestApplyMaterial_TraitsAndRarity(t *testing.T) {
	item := map[string]any{"stat_block": map[string]any{"traits": []any{
		map[string]any{"name": "Magical"}, map[string]any{"name": "Uncommon"},
	}}}
	// Dawnsilver-like: grants Silver + Rare + precious (precious classifies the material).
	attrs := json.RawMessage(`{"material_use_host":"weapon","material_grants_traits":["Silver","Rare","precious"]}`)
	if err := ApplyMaterial(item, attrs, eligibility.ItemFacts{Host: "weapon"}); err != nil {
		t.Fatal(err)
	}
	names := traitNames(item)
	if !names["Silver"] || !names["Magical"] {
		t.Fatalf("granted + existing traits missing: %v", names)
	}
	if names["precious"] {
		t.Error("precious must not propagate to the item")
	}
	// Rarity is the more restrictive of Uncommon (item) and Rare (material) → Rare;
	// the old Uncommon is dropped (an item has exactly one rarity).
	if !names["Rare"] || names["Uncommon"] {
		t.Fatalf("rarity not resolved to Rare: %v", names)
	}

	// Boundary: a material whose use-host doesn't match the item is refused.
	if err := ApplyMaterial(item, json.RawMessage(`{"material_use_host":"armor"}`),
		eligibility.ItemFacts{Host: "weapon"}); err == nil {
		t.Error("armor material on a weapon should be refused")
	}
}

func TestApplyMaterial_NoFabricatedCommonRarity(t *testing.T) {
	facts := eligibility.ItemFacts{Host: "weapon"}
	// A common item (no explicit rarity trait) + a material granting no rarity must
	// NOT gain a fabricated "Common" trait.
	item := map[string]any{"stat_block": map[string]any{"traits": []any{map[string]any{"name": "Versatile"}}}}
	if err := ApplyMaterial(item, json.RawMessage(`{"material_use_host":"weapon","material_grants_traits":["Silver"]}`), facts); err != nil {
		t.Fatal(err)
	}
	n := traitNames(item)
	if n["Common"] {
		t.Errorf("must not fabricate a Common rarity trait: %v", n)
	}
	if !n["Silver"] || !n["Versatile"] {
		t.Errorf("expected Versatile + Silver: %v", n)
	}

	// A common item + a material granting Rare DOES gain the Rare rarity.
	item2 := map[string]any{"stat_block": map[string]any{"traits": []any{}}}
	if err := ApplyMaterial(item2, json.RawMessage(`{"material_use_host":"weapon","material_grants_traits":["Rare"]}`), facts); err != nil {
		t.Fatal(err)
	}
	if !traitNames(item2)["Rare"] {
		t.Error("a granted rarity should be applied even when the item had none")
	}
}

func TestApplySpell_Boundaries(t *testing.T) {
	wand := json.RawMessage(`{"spell_holder":"wand","spell_max_rank":9,"spell_excluded_types":["cantrip","focus","ritual"]}`)
	newHolder := func() map[string]any {
		return map[string]any{"stat_block": map[string]any{"spell_slots": map[string]any{"holder": "wand"}}}
	}

	// A rank-5 non-cantrip fits and is written into the slot.
	h := newHolder()
	if err := ApplySpell(h, wand, "Fireball", 12345, 5, false); err != nil {
		t.Fatal(err)
	}
	slot := h["stat_block"].(map[string]any)["spell_slots"].(map[string]any)["spell"].(map[string]any)
	if slot["name"] != "Fireball" || slot["aonid"] != 12345 {
		t.Fatalf("spell slot = %+v", slot)
	}

	// Rank over max → refused.
	if err := ApplySpell(newHolder(), wand, "Wish", 0, 10, false); err == nil {
		t.Error("rank 10 > max 9 should be refused")
	}
	// Cantrip excluded → refused.
	if err := ApplySpell(newHolder(), wand, "Light", 0, 1, true); err == nil {
		t.Error("a wand excludes cantrips")
	}
	// Not a holder → refused.
	if err := ApplySpell(newHolder(), json.RawMessage(`{}`), "Light", 0, 1, false); err == nil {
		t.Error("a non-holder should be refused")
	}
}

func TestEnsureModifierTargets(t *testing.T) {
	item := map[string]any{"stat_block": map[string]any{"offense": map[string]any{
		"weapon_modes": []any{map[string]any{"weapon_type": "Melee"}}, // no modifiers key
	}}}
	effs := []template.Effect{{Operation: "add_modifier", Target: "$.offense.weapon_modes[*].modifiers"}}
	EnsureModifierTargets(item, effs)
	mode := item["stat_block"].(map[string]any)["offense"].(map[string]any)["weapon_modes"].([]any)[0].(map[string]any)
	if _, ok := mode["modifiers"].([]any); !ok {
		t.Fatalf("modifiers array not seeded: %+v", mode)
	}
	// Never fabricates intermediate objects: a target under a missing parent is left alone.
	bare := map[string]any{"stat_block": map[string]any{}}
	EnsureModifierTargets(bare, []template.Effect{{Operation: "add_modifier", Target: "$.defense.modifiers"}})
	if _, exists := bare["stat_block"].(map[string]any)["defense"]; exists {
		t.Error("must not create the intermediate defense object")
	}
	// Non-add_modifier effects are ignored.
	item2 := map[string]any{"stat_block": map[string]any{"offense": map[string]any{"weapon_modes": []any{map[string]any{}}}}}
	EnsureModifierTargets(item2, []template.Effect{{Operation: "replace", Target: "$.offense.weapon_modes[*].foo"}})
	m2 := item2["stat_block"].(map[string]any)["offense"].(map[string]any)["weapon_modes"].([]any)[0].(map[string]any)
	if _, exists := m2["foo"]; exists {
		t.Error("replace target must not be seeded")
	}
}

func traitNames(doc map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, t := range doc["stat_block"].(map[string]any)["traits"].([]any) {
		if tm, ok := t.(map[string]any); ok {
			if n, ok := tm["name"].(string); ok {
				out[n] = true
			}
		}
	}
	return out
}
