package template

import (
	"reflect"
	"testing"
)

func meleeWrapper(weapon, formula, dtype string, bonus float64, traits ...string) map[string]any {
	trs := make([]any, len(traits))
	for i, t := range traits {
		trs[i] = map[string]any{"name": t, "type": "stat_block_section"}
	}
	return map[string]any{
		"name": "Melee", "offensive_action_type": "attack",
		"subtype": "offensive_action", "type": "stat_block_section",
		"attack": map[string]any{
			"attack_type": "melee", "name": "Melee", "weapon": weapon,
			"subtype": "attack", "type": "stat_block_section",
			"bonus":  map[string]any{"bonuses": []any{bonus, bonus - 5, bonus - 10}},
			"traits": trs,
			"damage": []any{map[string]any{"damage_type": dtype, "formula": formula,
				"subtype": "attack_damage", "type": "stat_block_section"}},
		},
	}
}

func strikeStatBlock(wrappers ...map[string]any) map[string]any {
	oa := make([]any, len(wrappers))
	for i, w := range wrappers {
		oa[i] = w
	}
	return map[string]any{"offense": map[string]any{"offensive_actions": oa}}
}

func applyStrike(t *testing.T, sb map[string]any, item map[string]any) {
	t.Helper()
	eff := Effect{Operation: "add_strike", Target: "$.offense.offensive_actions", Item: item}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
}

func lastAttack(t *testing.T, sb map[string]any) map[string]any {
	t.Helper()
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	return oa[len(oa)-1].(map[string]any)["attack"].(map[string]any)
}

func TestAddStrike_CopiesLowestMeleeStrike(t *testing.T) {
	// jaws deals damage equal to the LOWEST melee Strike (1d4+2 club, not
	// the 2d8+4 sword); ranged attacks are not candidates.
	sword := meleeWrapper("greatsword", "2d8+4", "slashing", 15)
	club := meleeWrapper("club", "1d4+2", "bludgeoning", 15, "Agile")
	ranged := meleeWrapper("sling", "1d2", "bludgeoning", 12)
	ranged["attack"].(map[string]any)["attack_type"] = "ranged"
	sb := strikeStatBlock(sword, club, ranged)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing"})

	atk := lastAttack(t, sb)
	if atk["weapon"] != "jaws" || atk["attack_type"] != "melee" {
		t.Fatalf("attack = %#v", atk)
	}
	dmg := atk["damage"].([]any)[0].(map[string]any)
	if dmg["formula"] != "1d4+2" || dmg["damage_type"] != "piercing" {
		t.Errorf("damage = %#v", dmg)
	}
	if _, has := dmg["persistent"]; has {
		t.Errorf("non-persistent source must not produce a persistent entry")
	}
	// bonus copied from the source strike
	if b := atk["bonus"].(map[string]any)["bonuses"].([]any)[0]; b != float64(15) {
		t.Errorf("bonus = %v", b)
	}
}

func TestAddStrike_TraitsReplaceSourceTraits(t *testing.T) {
	club := meleeWrapper("club", "1d4+2", "bludgeoning", 10, "Magical", "Reach")
	sb := strikeStatBlock(club)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing",
		"traits": []any{"Agile", "Finesse"}})
	atk := lastAttack(t, sb)
	var names []string
	for _, tr := range atk["traits"].([]any) {
		names = append(names, tr.(map[string]any)["name"].(string))
	}
	if !reflect.DeepEqual(names, []string{"Agile", "Finesse"}) {
		t.Errorf("traits = %v", names)
	}
}

func TestAddStrike_NoTraitsGivenMeansNone(t *testing.T) {
	// A plain 'jaws Strike' bullet grants no weapon traits — the source
	// strike's traits must not leak onto the new natural attack.
	club := meleeWrapper("club", "1d4+2", "bludgeoning", 10, "Magical")
	sb := strikeStatBlock(club)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing"})
	atk := lastAttack(t, sb)
	if trs := atk["traits"].([]any); len(trs) != 0 {
		t.Errorf("traits should be empty, got %v", trs)
	}
}

func TestAddStrike_SkipIfWeaponsPresent(t *testing.T) {
	// Vampire: if the creature already has a jaws or fangs Strike, skip.
	jaws := meleeWrapper("jaws", "1d6+3", "piercing", 11)
	sb := strikeStatBlock(jaws)
	applyStrike(t, sb, map[string]any{"weapon": "fangs", "damage_type": "piercing",
		"skip_if_weapons": []any{"jaws", "fangs"}})
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	if len(oa) != 1 {
		t.Errorf("expected no new strike, got %d actions", len(oa))
	}
}

func TestAddStrike_DedupByOwnWeapon(t *testing.T) {
	jaws := meleeWrapper("jaws", "1d6+3", "piercing", 11)
	sb := strikeStatBlock(jaws)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing"})
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	if len(oa) != 1 {
		t.Errorf("expected dedup on existing jaws, got %d actions", len(oa))
	}
}

func TestAddStrike_NoMeleeStrikesSkips(t *testing.T) {
	ranged := meleeWrapper("longbow", "1d8", "piercing", 12)
	ranged["attack"].(map[string]any)["attack_type"] = "ranged"
	sb := strikeStatBlock(ranged)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing"})
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	if len(oa) != 1 {
		t.Errorf("no melee source: expected skip, got %d actions", len(oa))
	}
}

func TestAddStrike_MissingWeaponErrors(t *testing.T) {
	sb := strikeStatBlock(meleeWrapper("club", "1d4", "bludgeoning", 5))
	eff := Effect{Operation: "add_strike", Target: "$.offense.offensive_actions",
		Item: map[string]any{"damage_type": "piercing"}}
	if err := applyEffectGroup(sb, []Effect{eff}); err == nil {
		t.Error("expected error for add_strike without weapon")
	}
}

func TestAddStrike_SourceStrikeUntouched(t *testing.T) {
	// The source strike must survive unmodified: the new attack is a copy,
	// not an alias.
	club := meleeWrapper("club", "1d4+2", "bludgeoning", 10, "Magical")
	sb := strikeStatBlock(club)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing",
		"traits": []any{"Agile"}})
	src := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["attack"].(map[string]any)
	if src["weapon"] != "club" {
		t.Errorf("source weapon overwritten: %v", src["weapon"])
	}
	if src["damage"].([]any)[0].(map[string]any)["damage_type"] != "bludgeoning" {
		t.Errorf("source damage_type overwritten")
	}
	if names := src["traits"].([]any); len(names) != 1 {
		t.Errorf("source traits overwritten: %v", names)
	}
}

func TestAddStrike_RidersNotInherited(t *testing.T) {
	// Property-rune riders and effect entries stay on the source weapon.
	sword := meleeWrapper("flaming greatsword", "2d8+4", "slashing", 15)
	dmg := sword["attack"].(map[string]any)["damage"].([]any)
	dmg = append(dmg,
		map[string]any{"damage_type": "fire", "formula": "1d6", "subtype": "attack_damage", "type": "stat_block_section"},
		map[string]any{"effect": "Grab", "subtype": "attack_damage", "type": "stat_block_section"})
	sword["attack"].(map[string]any)["damage"] = dmg
	sb := strikeStatBlock(sword)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing"})
	got := lastAttack(t, sb)["damage"].([]any)
	if len(got) != 1 {
		t.Fatalf("riders inherited: %#v", got)
	}
	first := got[0].(map[string]any)
	if first["formula"] != "2d8+4" || first["damage_type"] != "piercing" {
		t.Errorf("primary damage = %#v", first)
	}
}

func TestAddStrike_ValuedTraitConfig(t *testing.T) {
	// Dwarf's clan dagger: Versatile B is a {name, value} trait.
	club := meleeWrapper("club", "1d4+2", "bludgeoning", 10)
	sb := strikeStatBlock(club)
	applyStrike(t, sb, map[string]any{"weapon": "clan dagger", "damage_type": "piercing",
		"traits": []any{"Agile", map[string]any{"name": "Versatile", "value": "B"}}})
	trs := lastAttack(t, sb)["traits"].([]any)
	if len(trs) != 2 {
		t.Fatalf("traits = %#v", trs)
	}
	v := trs[1].(map[string]any)
	if v["name"] != "Versatile" || v["value"] != "B" || v["type"] != "stat_block_section" {
		t.Errorf("valued trait = %#v", v)
	}
}

func TestAddStrike_WordLevelWeaponDedup(t *testing.T) {
	// "snake fangs" already includes fangs; capitalization must not matter.
	snake := meleeWrapper("Snake Fangs", "1d8+2", "piercing", 9)
	sb := strikeStatBlock(snake)
	applyStrike(t, sb, map[string]any{"weapon": "fangs", "damage_type": "piercing",
		"skip_if_weapons": []any{"jaws", "fangs"}})
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	if len(oa) != 1 {
		t.Errorf("word-level dedup failed, got %d actions", len(oa))
	}
}

func TestAddStrike_UnparseableFormulaNeverWins(t *testing.T) {
	// An effect-only or unparseable-formula strike must not be picked as
	// the lowest source.
	weird := meleeWrapper("tongue", "special", "bludgeoning", 8)
	club := meleeWrapper("club", "1d4+2", "bludgeoning", 10)
	sb := strikeStatBlock(weird, club)
	applyStrike(t, sb, map[string]any{"weapon": "jaws", "damage_type": "piercing"})
	got := lastAttack(t, sb)["damage"].([]any)[0].(map[string]any)
	if got["formula"] != "1d4+2" {
		t.Errorf("unparseable source won: %#v", got)
	}
}

func TestAddStrike_DamageTypeOmittedKeepsSource(t *testing.T) {
	club := meleeWrapper("club", "1d4+2", "bludgeoning", 10)
	sb := strikeStatBlock(club)
	applyStrike(t, sb, map[string]any{"weapon": "tail"})
	got := lastAttack(t, sb)["damage"].([]any)[0].(map[string]any)
	if got["damage_type"] != "bludgeoning" {
		t.Errorf("damage_type = %v", got["damage_type"])
	}
}

func TestAddStrike_NilConfigErrors(t *testing.T) {
	sb := strikeStatBlock(meleeWrapper("club", "1d4", "bludgeoning", 5))
	eff := Effect{Operation: "add_strike", Target: "$.offense.offensive_actions"}
	if err := applyEffectGroup(sb, []Effect{eff}); err == nil {
		t.Error("expected error for add_strike without item config")
	}
}

func TestAddStrike_MultiWordWeaponDedup(t *testing.T) {
	// "clan dagger" and "+1 clan dagger" both already count as a clan
	// dagger; word-subsequence matching, case-insensitive.
	for _, existing := range []string{"clan dagger", "+1 Clan Dagger"} {
		sb := strikeStatBlock(meleeWrapper(existing, "1d4+2", "piercing", 8))
		applyStrike(t, sb, map[string]any{"weapon": "clan dagger", "damage_type": "piercing"})
		oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
		if len(oa) != 1 {
			t.Errorf("%q: dedup failed, %d actions", existing, len(oa))
		}
	}
	// but "daggerfish" does not contain "dagger" as a word
	sb := strikeStatBlock(meleeWrapper("daggerfish tail", "1d4+2", "bludgeoning", 8))
	applyStrike(t, sb, map[string]any{"weapon": "dagger", "damage_type": "piercing"})
	if oa := sb["offense"].(map[string]any)["offensive_actions"].([]any); len(oa) != 2 {
		t.Errorf("substring should not dedup, got %d actions", len(oa))
	}
}

func TestAddStrike_PersistentPrimaryCarries(t *testing.T) {
	w := meleeWrapper("tendril", "4d12+13", "piercing", 20)
	w["attack"].(map[string]any)["damage"].([]any)[0].(map[string]any)["persistent"] = true
	sb := strikeStatBlock(w)
	applyStrike(t, sb, map[string]any{"weapon": "jaws"})
	got := lastAttack(t, sb)["damage"].([]any)[0].(map[string]any)
	if got["persistent"] != true {
		t.Errorf("persistent dropped: %#v", got)
	}
}

func TestAverageDamage(t *testing.T) {
	cases := []struct {
		formula string
		want    float64
	}{
		{"1d4+2", 4.5}, {"2d8+4", 13}, {"1d6", 3.5}, {"3", 3}, {"special", -1}, {"", -1},
	}
	for _, c := range cases {
		if got := averageDamage(c.formula); got != c.want {
			t.Errorf("%s: got %v want %v", c.formula, got, c.want)
		}
	}
}

// o9hy: add_item to $.offense.offensive_actions[*].attack.traits must reach every
// strike, not just the ones that already carry a traits array. Before the fix,
// applyWildcardEffects only created/resolved the leaf when NO element resolved, so on
// a creature whose strikes are mixed (some have attack.traits, some don't) the add
// landed only on the ones that already had the array. This is the ghost/shadow/phantom
// strike-trait case (adding e.g. a magical/ethereal trait to all strikes).
func TestAddItem_WildcardTraits_LandsOnStrikesWithoutTraitsArray(t *testing.T) {
	withTraits := meleeWrapper("club", "1d6", "bludgeoning", 12, "agile") // has attack.traits
	noTraits := map[string]any{
		"name": "Jaws", "offensive_action_type": "attack",
		"subtype": "offensive_action", "type": "stat_block_section",
		"attack": map[string]any{
			"attack_type": "melee", "name": "Jaws", "weapon": "jaws",
			"subtype": "attack", "type": "stat_block_section",
			"bonus": map[string]any{"bonuses": []any{10.0, 5.0, 0.0}},
			// deliberately NO "traits" key
			"damage": []any{map[string]any{"damage_type": "piercing", "formula": "1d6",
				"subtype": "attack_damage", "type": "stat_block_section"}},
		},
	}
	creature := map[string]any{"stat_block": strikeStatBlock(withTraits, noTraits)}

	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "traits",
			Effects: []Effect{{
				Operation: "add_item",
				Target:    "$.offense.offensive_actions[*].attack.traits",
				Item:      map[string]any{"name": "magical", "type": "stat_block_section"},
			}},
		}},
	}}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	oa := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)

	club := oa[0].(map[string]any)["attack"].(map[string]any)["traits"]
	if got := namesOf(club); !reflect.DeepEqual(got, []string{"agile", "magical"}) {
		t.Errorf("club strike traits = %v, want [agile magical]", got)
	}
	// The strike that had no traits array must have one created with the added trait.
	jawsAttack := oa[1].(map[string]any)["attack"].(map[string]any)
	traits, ok := jawsAttack["traits"]
	if !ok {
		t.Fatalf("jaws strike never got a traits array — add_item skipped it (the o9hy bug)")
	}
	if got := namesOf(traits); !reflect.DeepEqual(got, []string{"magical"}) {
		t.Errorf("jaws strike traits = %v, want [magical]", got)
	}
}
