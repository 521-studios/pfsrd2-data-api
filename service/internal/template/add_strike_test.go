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

func TestAverageDamage(t *testing.T) {
	cases := []struct {
		formula string
		want    float64
	}{
		{"1d4+2", 4.5}, {"2d8+4", 13}, {"1d6", 3.5}, {"3", 3},
	}
	for _, c := range cases {
		if got := averageDamage(c.formula); got != c.want {
			t.Errorf("%s: got %v want %v", c.formula, got, c.want)
		}
	}
}
