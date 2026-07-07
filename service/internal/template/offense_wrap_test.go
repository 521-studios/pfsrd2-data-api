package template

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func TestAddItems_OffensiveAbilityGetsWrapped(t *testing.T) {
	// Plain abilities appended to offensive_actions must take the wrapper
	// shape real creatures use ({name, ability, offensive_action_type}),
	// or the display layer cannot render them.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Drag Along", "offensive")},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	oa := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)
	if len(oa) != 1 {
		t.Fatalf("offensive_actions = %v", oa)
	}
	w := oa[0].(map[string]any)
	if w["name"] != "Drag Along" || w["offensive_action_type"] != "ability" ||
		w["subtype"] != "offensive_action" || w["type"] != "stat_block_section" {
		t.Errorf("wrapper shape wrong: %#v", w)
	}
	inner, ok := w["ability"].(map[string]any)
	if !ok || inner["name"] != "Drag Along" {
		t.Errorf("inner ability wrong: %#v", w["ability"])
	}
}

func TestAddItems_AlreadyWrappedOffensiveEntryUntouched(t *testing.T) {
	// An item that already looks like a wrapper (has ability/attack/spells)
	// is appended as-is.
	wrapped := map[string]any{
		"name": "Prewrapped", "subtype": "offensive_action",
		"type": "stat_block_section", "offensive_action_type": "ability",
		"ability": map[string]any{"name": "Prewrapped", "type": "stat_block_section"},
	}
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{wrapped},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities[?(@.name=='Prewrapped')]",
				Target:    "$.offense.offensive_actions",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	oa := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)
	got := oa[0].(map[string]any)
	if _, double := got["ability"].(map[string]any)["ability"]; double {
		t.Errorf("wrapper was wrapped again: %#v", got)
	}
}

func TestSetReach_ReducesReachTraitOnMeleeAttack(t *testing.T) {
	// Miniature semantics: a melee attack with a Reach trait has its value
	// reduced to the effect's value in feet.
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"name": "Melee",
					"attack": map[string]any{
						"attack_type": "melee",
						"name":        "Melee",
						"traits": []any{
							map[string]any{"name": "Reach", "value": "20 feet",
								"type": "stat_block_section"},
							map[string]any{"name": "Magical", "type": "stat_block_section"},
						},
					},
				},
				map[string]any{
					"name": "Melee",
					"attack": map[string]any{
						"attack_type": "melee",
						"name":        "Claw",
						"traits":      []any{},
					},
				},
			},
		},
	}
	eff := Effect{
		Operation: "set_reach",
		Target:    "$.offense.offensive_actions[*].attack",
		Value:     float64(5),
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	traits := oa[0].(map[string]any)["attack"].(map[string]any)["traits"].([]any)
	reach := traits[0].(map[string]any)
	if reach["value"] != "5 feet" {
		t.Errorf("reach value = %v", reach["value"])
	}
	if _, has := traits[1].(map[string]any)["value"]; has {
		t.Errorf("non-Reach trait gained a value: %#v", traits[1])
	}
	// No Reach trait: untouched, and no stray fields added.
	claw := oa[1].(map[string]any)["attack"].(map[string]any)
	if !reflect.DeepEqual(claw["traits"], []any{}) {
		t.Errorf("claw traits changed: %v", claw["traits"])
	}
	if _, has := claw["reach"]; has {
		t.Errorf("stray reach field added to claw")
	}
}

func TestSetReach_NonMapTargetNoOp(t *testing.T) {
	rv := ResolvedValue{}
	if err := applySetReach(rv, Effect{Operation: "set_reach", Value: float64(5)}); err != nil {
		t.Errorf("nil target should no-op, got %v", err)
	}
}

func TestSetReach_TextUntouched(t *testing.T) {
	// Reach trait text is glossary rules prose — set_reach must not
	// rewrite it (substring replacement corrupts real texts).
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"name": "Melee",
					"attack": map[string]any{
						"attack_type": "melee",
						"traits": []any{
							map[string]any{"name": "Reach", "value": "15 feet",
								"text": "Reach 15 feet", "type": "stat_block_section"},
						},
					},
				},
			},
		},
	}
	eff := Effect{
		Operation: "set_reach",
		Target:    "$.offense.offensive_actions[*].attack",
		Value:     float64(5),
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	tr := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["attack"].(map[string]any)["traits"].([]any)[0].(map[string]any)
	if tr["value"] != "5 feet" || tr["text"] != "Reach 15 feet" {
		t.Errorf("trait = %#v (text must be untouched)", tr)
	}
}

func TestSetReach_NonNumericValueErrors(t *testing.T) {
	rv := ResolvedValue{}
	err := applySetReach(rv, Effect{Operation: "set_reach", Value: "five"})
	if err == nil || !strings.Contains(err.Error(), "must be numeric") {
		t.Errorf("want numeric-value error even for nil targets, got %v", err)
	}
}

func TestWrapOffensiveAbility_PassthroughKeys(t *testing.T) {
	for _, k := range []string{"ability", "attack", "spells", "mythic_ability"} {
		item := map[string]any{"name": "X", k: map[string]any{"name": "X"}}
		got := wrapOffensiveAbility(item).(map[string]any)
		if _, doubled := got["ability"].(map[string]any); doubled && k != "ability" {
			t.Errorf("key %s: wrapper was wrapped again: %#v", k, got)
		}
		if k == "ability" {
			if _, inner := got["ability"].(map[string]any)["ability"]; inner {
				t.Errorf("ability wrapper double-wrapped")
			}
		}
	}
	if got := wrapOffensiveAbility("bare string"); got != "bare string" {
		t.Errorf("non-map input should pass through, got %#v", got)
	}
}

func TestAddItems_UnfilteredOffensiveRouteWraps(t *testing.T) {
	// The unfiltered source route must wrap bare offensive abilities too.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{ability("Sand Burst", "offensive")},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities",
				Target:    "$.offense.offensive_actions",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	oa := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)
	w := oa[0].(map[string]any)
	if _, ok := w["ability"].(map[string]any); !ok || w["offensive_action_type"] != "ability" {
		t.Errorf("unfiltered route did not wrap: %#v", w)
	}
}

func TestValueFromAggregateWithDivision(t *testing.T) {
	// sandbound: burrow speed = half the fastest speed, floored.
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "walk", "value": float64(25)},
					map[string]any{"movement_type": "fly", "value": float64(45)},
				},
			},
		},
	}
	got, err := evaluateValueFrom(sb, "$.offense.speed.movement[*].value | max / 2", nil)
	if err != nil || got != float64(20) {
		t.Errorf("got %v err %v want 20 (5-foot floor)", got, err)
	}
	min := floatPtr(30)
	got, err = evaluateValueFrom(sb, "$.offense.speed.movement[*].value | max / 2", min)
	if err != nil || got != float64(30) {
		t.Errorf("minimum not applied: got %v err %v", got, err)
	}
}

func TestValueFromSingleDivisionFloorsToFive(t *testing.T) {
	// 35 / 2 = 17.5 -> 15 under the PF2 halved-Speed convention.
	sb := map[string]any{
		"offense": map[string]any{"speed": map[string]any{"movement": []any{
			map[string]any{"movement_type": "walk", "value": float64(35)},
		}}},
	}
	got, err := evaluateValueFrom(sb, "$.offense.speed.movement[?(@.movement_type=='walk')].value / 2", nil)
	if err != nil || got != float64(15) {
		t.Errorf("got %v err %v want 15", got, err)
	}
}

func TestValueFromMinimumOnNonDivisionForms(t *testing.T) {
	sb := map[string]any{
		"statistics": map[string]any{"skills": []any{
			map[string]any{"name": "Stealth", "value": float64(4)},
		}},
		"creature_type": map[string]any{"level": float64(2)},
	}
	min := floatPtr(9)
	got, err := evaluateValueFrom(sb, "$.statistics.skills[*].value | max", min)
	if err != nil || got != float64(9) {
		t.Errorf("aggregate minimum: got %v err %v want 9", got, err)
	}
	got, err = evaluateValueFrom(sb, "$.creature_type.level", min)
	if err != nil || got != float64(9) {
		t.Errorf("plain-path minimum: got %v err %v want 9", got, err)
	}
}

func TestValueFromPlainPath(t *testing.T) {
	sb := map[string]any{"creature_type": map[string]any{"level": float64(7), "size": "Large"}}
	if got, err := evaluateValueFrom(sb, "$.creature_type.level", nil); err != nil || got != float64(7) {
		t.Errorf("numeric: got %v err %v", got, err)
	}
	if got, err := evaluateValueFrom(sb, "$.creature_type.size", nil); err != nil || got != "Large" {
		t.Errorf("non-numeric passthrough: got %v err %v", got, err)
	}
	_, err := evaluateValueFrom(sb, "$.creature_type.missing", nil)
	if !errorsIs(err, ErrValueFromPath) {
		t.Errorf("missing path should be sentinel, got %v", err)
	}
}

func TestValueFromSentinelProducers(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{"offensive_actions": []any{
			map[string]any{"attack": map[string]any{"damage": []any{
				map[string]any{"formula": "1d6", "damage_type": "piercing"},
			}}},
		}},
		"creature_type": map[string]any{"level": "seven"},
	}
	cases := []struct {
		expr string
		want error
	}{
		{"$.statistics.skills[*].value | max", ErrValueFromPath},                                       // aggregate empty
		{"$.offense.offensive_actions[*].attack.damage | min", ErrValueFromShape},                      // dicts, not numbers
		{"$.offense.offensive_actions[?(@.name=='x')].attack.damage[0].formula / 2", ErrValueFromPath}, // single missing
		{"$.creature_type.level | high_for_level", ErrValueFromShape},                                  // non-numeric level
		{"$.statistics.skills[*].value | max / 2", ErrValueFromPath},                                   // division over empty
		{"$.creature_type.level / 2", ErrValueFromShape},                                               // single-path non-numeric
	}
	for i, c := range cases {
		_, err := evaluateValueFrom(sb, c.expr, nil)
		if !errorsIs(err, c.want) {
			t.Errorf("case %d (%s): got %v want sentinel %v", i, c.expr, err, c.want)
		}
	}
	// missing creature level needs its own fixture: the shared one has a
	// (non-numeric) level, which is the shape case, not the path case
	_, err := evaluateValueFrom(map[string]any{}, "$.creature_type.level | high_for_level", nil)
	if !errorsIs(err, ErrValueFromPath) {
		t.Errorf("missing level: got %v want path sentinel", err)
	}
	// an unknown aggregate operator is a malformed template: hard error,
	// never a sentinel skip
	_, err = evaluateValueFrom(sb, "$.statistics.skills[*].value | median", nil)
	if err == nil || errorsIs(err, ErrValueFromPath) || errorsIs(err, ErrValueFromShape) {
		t.Errorf("unknown operator: got %v want non-sentinel error", err)
	}
}

func TestApplyEffect_ShapeSentinelWarnSkips(t *testing.T) {
	// The warn-skip arm in applySingleEffect: a shape mismatch skips the
	// effect without erroring and without mutating the stat block.
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{map[string]any{"attack": map[string]any{"damage": []any{
				map[string]any{"formula": "1d6", "damage_type": "piercing"},
			}}}},
			"speed": map[string]any{"movement": []any{}},
		},
	}
	eff := Effect{
		Operation: "add_item", Target: "$.offense.speed.movement",
		Item:      map[string]any{"movement_type": "burrow", "name": "burrow", "subtype": "speed", "type": "stat_block_section"},
		ValueFrom: "$.offense.offensive_actions[*].attack.damage | min",
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("shape mismatch must warn-skip, got %v", err)
	}
	mv := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)
	if len(mv) != 0 {
		t.Errorf("effect should have been skipped, movement = %v", mv)
	}
}

func TestSetReach_NeverRaisesReach(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{"offensive_actions": []any{
			map[string]any{"name": "Melee", "attack": map[string]any{
				"attack_type": "melee",
				"traits": []any{map[string]any{"name": "Reach", "value": "0 feet",
					"type": "stat_block_section"}},
			}},
		}},
	}
	eff := Effect{Operation: "set_reach", Target: "$.offense.offensive_actions[*].attack", Value: float64(5)}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	tr := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["attack"].(map[string]any)["traits"].([]any)[0].(map[string]any)
	if tr["value"] != "0 feet" {
		t.Errorf("reach was raised: %v", tr["value"])
	}
}

func TestValueFromDivisionErrors(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{"speed": map[string]any{"movement": []any{
			map[string]any{"movement_type": "walk", "value": float64(25)},
		}}},
	}
	for _, expr := range []string{
		"$.offense.speed.movement[*].value | max / 0",
		"$.offense.speed.movement[*].value | max / zero",
		"$.offense.speed.movement[?(@.movement_type=='walk')].value / 0",
	} {
		if _, err := evaluateValueFrom(sb, expr, nil); err == nil {
			t.Errorf("expected divisor error for %q", expr)
		}
	}
}

func TestValueFromTemplateErrorsPropagate(t *testing.T) {
	// A malformed expression must fail the apply; a missing creature field
	// must skip silently. The sentinel separates the two.
	creature := map[string]any{"stat_block": map[string]any{
		"creature_type": map[string]any{"level": float64(1)},
		"senses":        map[string]any{},
		"defense":       map[string]any{"hitpoints": []any{map[string]any{"hp": float64(10)}}},
		"offense": map[string]any{
			"offensive_actions": []any{},
			"speed":             map[string]any{"movement": []any{map[string]any{"movement_type": "swim", "value": float64(30)}}},
		},
	}}
	badTmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{Changes: []Change{{
		ChangeCategory: "speed",
		Effects: []Effect{{
			Operation: "add_item", Target: "$.offense.speed.movement",
			Item:      map[string]any{"movement_type": "burrow", "name": "burrow", "subtype": "speed", "type": "stat_block_section"},
			ValueFrom: "$.offense.speed.movement[*].value | max / 0",
		}},
	}}}}
	if _, err := Apply(creature, badTmpl); err == nil {
		t.Error("malformed value_from must propagate")
	}
	missingTmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{Changes: []Change{{
		ChangeCategory: "speed",
		Effects: []Effect{{
			Operation: "add_item", Target: "$.offense.speed.movement",
			Item:      map[string]any{"movement_type": "swim2", "name": "swim2", "subtype": "speed", "type": "stat_block_section"},
			ValueFrom: "$.offense.speed.movement[?(@.movement_type=='walk')].value / 2",
		}},
	}}}}
	if _, err := Apply(creature, missingTmpl); err != nil {
		t.Errorf("missing creature field must skip, got %v", err)
	}
}
