package template

import (
	"errors"
	"fmt"
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
								"level": float64(8), "level_text": "(8th)", "cantrips": true,
								"spells": []any{
									map[string]any{"name": "daze", "subtype": "spell"},
								},
							},
							map[string]any{
								"level": float64(3), "level_text": "(3rd)", "constant": true,
								"spells": []any{
									map[string]any{"name": "truespeech", "subtype": "spell", "count_text": "at will"},
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
		return nil, fmt.Errorf("%w: no doc for %s", ErrSpellNotFound, gameID)
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

func TestSpellSwapConstantGroupIsRanked(t *testing.T) {
	// Constant groups share the "(3rd)" level_text shape with cantrip
	// groups but are ranked spells — a same-rank spell must be accepted
	// and a cantrip rejected.
	effs, err := spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "truespeech", ReplacementGameID: "gid-wall"}}, fakeResolver(t))
	if err != nil {
		t.Fatalf("constant-slot swap rejected: %v", err)
	}
	if effs[0].Value.(map[string]any)["name"] != "wall of wind" {
		t.Errorf("bad constant swap: %+v", effs[0].Value)
	}
	_, err = spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "truespeech", ReplacementGameID: "gid-galeblast"}}, fakeResolver(t))
	if err == nil || !strings.Contains(err.Error(), "is a cantrip") {
		t.Errorf("cantrip into constant slot should be rejected, got %v", err)
	}
}

func TestSpellSwapPreservesCountMarkers(t *testing.T) {
	effs, err := spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "truespeech", ReplacementGameID: "gid-wall"}}, fakeResolver(t))
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	val := effs[0].Value.(map[string]any)
	if val["count_text"] != "at will" {
		t.Errorf("count_text not preserved: %v", val)
	}
}

func TestSpellSwapAmbiguousMultiRankName(t *testing.T) {
	sb := swapCreatureSB()
	lists := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["spells"].(map[string]any)["spell_list"].([]any)
	lists[1].(map[string]any)["spells"] = append(
		lists[1].(map[string]any)["spells"].([]any),
		map[string]any{"name": "bane", "subtype": "spell"},
	)
	_, err := spellSwapEffects(sb, swapRef(), []SpellSwap{{From: "bane", ReplacementGameID: "gid-gust"}}, fakeResolver(t))
	if err == nil || !strings.Contains(err.Error(), "multiple ranks") {
		t.Errorf("expected ambiguity rejection, got %v", err)
	}
}

func TestSpellSwapNoConstraint(t *testing.T) {
	ref := swapRef()
	delete(ref.eff.Selection, "constraint")
	_, err := spellSwapEffects(swapCreatureSB(), ref, []SpellSwap{{From: "bane", ReplacementGameID: "gid-gust"}}, fakeResolver(t))
	if err == nil || !strings.Contains(err.Error(), "no trait constraint") {
		t.Errorf("expected constraint rejection, got %v", err)
	}
}

func TestSpellSwapNotASpellEntry(t *testing.T) {
	resolve := SpellResolver(func(string) (map[string]any, error) {
		return map[string]any{"name": "Some Feat", "feat": map[string]any{}}, nil
	})
	_, err := spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "bane", ReplacementGameID: "gid-x"}}, resolve)
	if err == nil || !strings.Contains(err.Error(), "not a spell") {
		t.Errorf("expected not-a-spell rejection, got %v", err)
	}
}

func TestSpellSwapInfraErrorIsNotBadSelection(t *testing.T) {
	resolve := SpellResolver(func(string) (map[string]any, error) {
		return nil, errors.New("s3 timeout")
	})
	_, err := spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "bane", ReplacementGameID: "gid-x"}}, resolve)
	if err == nil || errors.Is(err, ErrBadSelection) {
		t.Errorf("infra error must not be ErrBadSelection (would 400), got %v", err)
	}
	notFound := SpellResolver(func(string) (map[string]any, error) {
		return nil, fmt.Errorf("%w: no entry", ErrSpellNotFound)
	})
	_, err = spellSwapEffects(swapCreatureSB(), swapRef(), []SpellSwap{{From: "bane", ReplacementGameID: "gid-x"}}, notFound)
	if err == nil || !errors.Is(err, ErrBadSelection) {
		t.Errorf("not-found must be ErrBadSelection, got %v", err)
	}
}

func TestSpellSwapThroughApplyWithSelections(t *testing.T) {
	creature := map[string]any{"stat_block": swapCreatureSB()}
	tmpl := TemplateJSON{
		MonsterTemplate: MonsterTemplate{
			Changes: []Change{{
				ChangeCategory: "spells",
				Text:           "- Replace spells with air spells of the same rank.",
				Effects: []Effect{{
					Operation: "select",
					Target:    "$.offense.offensive_actions[*].spells.spell_list[*].spells",
					Selection: map[string]any{"action": "replace", "constraint": "air", "type": "select"},
				}},
			}},
		},
	}
	chosen := []SelectionChoice{{
		ID: "c0/e0",
		SpellSwaps: []SpellSwap{
			{From: "bane", ReplacementGameID: "gid-gust"},
			{From: "daze", ReplacementGameID: "gid-galeblast"},
		},
	}}
	res, err := ApplyWithSelectionsResolver(creature, tmpl, chosen, fakeResolver(t))
	if err != nil {
		t.Fatalf("ApplyWithSelectionsResolver: %v", err)
	}
	sb := res.Creature["stat_block"].(map[string]any)
	lists := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["spells"].(map[string]any)["spell_list"].([]any)
	if lists[0].(map[string]any)["spells"].([]any)[0].(map[string]any)["name"] != "gust of wind" {
		t.Errorf("rank-1 swap not applied through selections")
	}
	if lists[1].(map[string]any)["spells"].([]any)[0].(map[string]any)["name"] != "gale blast" {
		t.Errorf("cantrip swap not applied through selections")
	}
	if len(res.PatchDoc.AppliedSelections) != 1 || res.PatchDoc.AppliedSelections[0] != "c0/e0" {
		t.Errorf("applied_selections echo wrong: %v", res.PatchDoc.AppliedSelections)
	}
	found := false
	for _, g := range res.PatchDoc.AppliedPatches {
		if g.SelectionID == "c0/e0" && len(g.Operations) > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("no patch group attributed to the selection")
	}
}
