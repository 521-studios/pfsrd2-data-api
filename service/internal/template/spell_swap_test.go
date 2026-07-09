package template

import (
	"errors"
	"strings"
	"testing"
)

func swapCreatureSB() map[string]any {
	return map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"name": "Occult Spontaneous Spells",
					"spells": map[string]any{
						"spell_list": []any{
							map[string]any{
								"level": float64(1), "level_text": "1st",
								"spells": []any{
									map[string]any{"name": "bane", "subtype": "spell"},
									map[string]any{"name": "fear", "subtype": "spell"},
								},
							},
							map[string]any{
								"level": float64(8), "level_text": "(8th)",
								"spells": []any{
									map[string]any{"name": "daze", "subtype": "spell"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func swapRef() selectRef {
	return selectRef{
		change: Change{ChangeCategory: "spells", Text: "- Replace spells with air spells of the same rank."},
		eff: Effect{
			Operation: "select",
			Target:    "$.offense.offensive_actions[*].spells.spell_list[*].spells",
			Selection: map[string]any{"action": "replace", "constraint": "air", "type": "select"},
		},
	}
}

func fakeResolver(t *testing.T) SpellResolver {
	docs := map[string]map[string]any{
		"gid-gust": {
			"name": "Gust of Wind", "aonid": float64(152),
			"spell": map[string]any{
				"rank": float64(1),
				"traits": []any{
					map[string]any{"name": "Air"}, map[string]any{"name": "Evocation"},
				},
			},
		},
		"gid-wall": {
			"name": "Wall of Wind", "aonid": float64(371),
			"spell": map[string]any{
				"rank":   float64(3),
				"traits": []any{map[string]any{"name": "Air"}},
			},
		},
		"gid-galeblast": {
			"name": "Gale Blast", "aonid": float64(559),
			"spell": map[string]any{
				"rank": float64(1),
				"traits": []any{
					map[string]any{"name": "Air"}, map[string]any{"name": "Cantrip"},
				},
			},
		},
		"gid-fireball": {
			"name": "Fireball", "aonid": float64(111),
			"spell": map[string]any{
				"rank":   float64(3),
				"traits": []any{map[string]any{"name": "Fire"}},
			},
		},
	}
	return func(gameID string) (map[string]any, error) {
		if d, ok := docs[gameID]; ok {
			return d, nil
		}
		return nil, errors.New("not found")
	}
}

func TestSpellSwapBuildsReplaceEffect(t *testing.T) {
	sb := swapCreatureSB()
	effs, err := spellSwapEffects(sb, swapRef(), []SpellSwap{{From: "bane", ReplacementGameID: "gid-gust"}}, fakeResolver(t))
	if err != nil {
		t.Fatalf("spellSwapEffects: %v", err)
	}
	if len(effs) != 1 {
		t.Fatalf("expected 1 effect, got %d", len(effs))
	}
	e := effs[0]
	if e.Operation != "replace" || !strings.Contains(e.Target, "[?(@.name=='bane')]") {
		t.Errorf("bad effect target/op: %+v", e)
	}
	val := e.Value.(map[string]any)
	if val["name"] != "gust of wind" {
		t.Errorf("value name = %v", val["name"])
	}
	link := val["links"].([]any)[0].(map[string]any)
	if link["aonid"] != float64(152) || link["game-obj"] != "Spells" {
		t.Errorf("bad link: %v", link)
	}
}

func TestSpellSwapAppliesEndToEnd(t *testing.T) {
	sb := swapCreatureSB()
	effs, err := spellSwapEffects(sb, swapRef(), []SpellSwap{{From: "bane", ReplacementGameID: "gid-gust"}}, fakeResolver(t))
	if err != nil {
		t.Fatalf("spellSwapEffects: %v", err)
	}
	if err := applyEffectGroup(sb, effs); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	lists := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["spells"].(map[string]any)["spell_list"].([]any)
	names := []string{}
	for _, sp := range lists[0].(map[string]any)["spells"].([]any) {
		names = append(names, sp.(map[string]any)["name"].(string))
	}
	if names[0] != "gust of wind" || names[1] != "fear" {
		t.Errorf("swap not applied: %v", names)
	}
}

func TestSpellSwapRejections(t *testing.T) {
	cases := []struct {
		name string
		swap SpellSwap
		want string
	}{
		{"wrong trait", SpellSwap{From: "bane", ReplacementGameID: "gid-fireball"}, "lacks the air trait"},
		{"wrong rank", SpellSwap{From: "bane", ReplacementGameID: "gid-wall"}, "same rank required"},
		{"cantrip into slot", SpellSwap{From: "bane", ReplacementGameID: "gid-galeblast"}, "is a cantrip"},
		{"spell into cantrip slot", SpellSwap{From: "daze", ReplacementGameID: "gid-gust"}, "not a cantrip"},
		{"unknown from", SpellSwap{From: "wish", ReplacementGameID: "gid-gust"}, "no spell"},
		{"unknown replacement", SpellSwap{From: "bane", ReplacementGameID: "gid-nope"}, "not found"},
		{"missing fields", SpellSwap{}, "requires from"},
	}
	for _, c := range cases {
		_, err := spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{c.swap}, fakeResolver(t))
		if err == nil || !errors.Is(err, ErrBadSelection) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: expected ErrBadSelection containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestSpellSwapCantripToCantrip(t *testing.T) {
	effs, err := spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "daze", ReplacementGameID: "gid-galeblast"}}, fakeResolver(t))
	if err != nil {
		t.Fatalf("cantrip swap rejected: %v", err)
	}
	if len(effs) != 1 || effs[0].Value.(map[string]any)["name"] != "gale blast" {
		t.Errorf("bad cantrip swap effect: %+v", effs)
	}
}

func TestSpellSwapNoResolver(t *testing.T) {
	_, err := spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "bane", ReplacementGameID: "gid-gust"}}, nil)
	if err == nil || !errors.Is(err, ErrBadSelection) {
		t.Errorf("expected ErrBadSelection without resolver, got %v", err)
	}
}

func TestSpellSwapWrongSelectionAction(t *testing.T) {
	ref := swapRef()
	ref.eff.Selection["action"] = "add"
	_, err := spellSwapEffects(swapCreatureSB(), ref, []SpellSwap{{From: "bane", ReplacementGameID: "gid-gust"}}, fakeResolver(t))
	if err == nil || !strings.Contains(err.Error(), "does not accept spell swaps") {
		t.Errorf("expected action rejection, got %v", err)
	}
}

func TestSpellSwapApostropheName(t *testing.T) {
	sb := swapCreatureSB()
	lists := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["spells"].(map[string]any)["spell_list"].([]any)
	lists[0].(map[string]any)["spells"].([]any)[0].(map[string]any)["name"] = "heroes' feast"
	effs, err := spellSwapEffects(sb, swapRef(), []SpellSwap{{From: "heroes' feast", ReplacementGameID: "gid-gust"}}, fakeResolver(t))
	if err != nil {
		t.Fatalf("apostrophe swap: %v", err)
	}
	if err := applyEffectGroup(sb, effs); err != nil {
		t.Fatalf("apply with apostrophe filter: %v", err)
	}
	name := lists[0].(map[string]any)["spells"].([]any)[0].(map[string]any)["name"]
	if name != "gust of wind" {
		t.Errorf("apostrophe-name swap did not land: %v", name)
	}
}
