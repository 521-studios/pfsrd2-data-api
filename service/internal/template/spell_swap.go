package template

import (
	"fmt"
	"strings"
)

// SpellResolver fetches a full entry document by game_id. The API layer
// injects a DB+S3-backed implementation; tests inject fixtures. Spell swaps
// require it — without a resolver they are rejected as bad selections.
type SpellResolver func(gameID string) (map[string]any, error)

// SpellSwap is the client's answer to a replace-action spell selection:
// which creature spell to remove and which spell (by game_id) replaces it.
// All validation and effect construction happens here — the client never
// builds paths, effects, or link objects.
type SpellSwap struct {
	From              string `json:"from"`
	ReplacementGameID string `json:"replacement_game_id"`
}

// spellSwapEffects validates a choice's swaps against the selection's
// constraint and the creature's spell lists, and builds one replace effect
// per swap. Every failure is an ErrBadSelection — these are caller inputs.
func spellSwapEffects(statBlock map[string]any, ref selectRef, swaps []SpellSwap, resolve SpellResolver) ([]Effect, error) {
	action, _ := ref.eff.Selection["action"].(string)
	if action != "replace" {
		return nil, fmt.Errorf("%w: selection does not accept spell swaps (action %q)", ErrBadSelection, action)
	}
	constraint, _ := ref.eff.Selection["constraint"].(string)
	if constraint == "" {
		return nil, fmt.Errorf("%w: selection has no trait constraint", ErrBadSelection)
	}
	if resolve == nil {
		return nil, fmt.Errorf("%w: spell swaps are not supported here (no spell resolver)", ErrBadSelection)
	}

	var effects []Effect
	for _, sw := range swaps {
		if sw.From == "" || sw.ReplacementGameID == "" {
			return nil, fmt.Errorf("%w: spell swap requires from and replacement_game_id", ErrBadSelection)
		}
		slotRank, slotIsCantrip, found := findSpellSlot(statBlock, sw.From)
		if !found {
			return nil, fmt.Errorf("%w: creature has no spell %q", ErrBadSelection, sw.From)
		}

		doc, err := resolve(sw.ReplacementGameID)
		if err != nil || doc == nil {
			return nil, fmt.Errorf("%w: replacement spell %q not found", ErrBadSelection, sw.ReplacementGameID)
		}
		name, _ := doc["name"].(string)
		spell, _ := doc["spell"].(map[string]any)
		if name == "" || spell == nil {
			return nil, fmt.Errorf("%w: entry %q is not a spell", ErrBadSelection, sw.ReplacementGameID)
		}
		traits := spellTraitNames(spell["traits"])
		if !containsFold(traits, constraint) {
			return nil, fmt.Errorf("%w: %s lacks the %s trait", ErrBadSelection, name, constraint)
		}
		repIsCantrip := containsFold(traits, "cantrip")
		repRank, hasRank := toFloat64(spell["rank"])
		switch {
		case slotIsCantrip && !repIsCantrip:
			return nil, fmt.Errorf("%w: %s is not a cantrip (cantrips replace cantrips)", ErrBadSelection, name)
		case !slotIsCantrip && repIsCantrip:
			return nil, fmt.Errorf("%w: %s is a cantrip; the replaced spell is rank %d", ErrBadSelection, name, slotRank)
		case !slotIsCantrip && (!hasRank || int(repRank) != slotRank):
			return nil, fmt.Errorf("%w: %s is rank %v, the replaced spell is rank %d (same rank required)",
				ErrBadSelection, name, spell["rank"], slotRank)
		}

		lower := strings.ToLower(name)
		effects = append(effects, Effect{
			Operation: "replace",
			Target: fmt.Sprintf("%s[?(@.name=='%s')]",
				ref.eff.Target, escapeFilterName(strings.ToLower(sw.From))),
			Value: map[string]any{
				"name":    lower,
				"subtype": "spell",
				"type":    "stat_block_section",
				"links": []any{map[string]any{
					"name": lower, "alt": lower, "aonid": doc["aonid"],
					"game-obj": "Spells", "type": "link",
				}},
			},
		})
	}
	return effects, nil
}

// findSpellSlot locates a spell by name in the creature's spell lists and
// reports the slot's rank and whether it is a cantrip group. Heightened
// cantrip groups carry a numeric level ("Cantrips (5th)" has level 5), so
// the level_text — not the level — distinguishes cantrips from ranks.
func findSpellSlot(statBlock map[string]any, name string) (rank int, isCantrip, found bool) {
	offense, _ := statBlock["offense"].(map[string]any)
	actions, _ := offense["offensive_actions"].([]any)
	for _, oa := range actions {
		m, _ := oa.(map[string]any)
		spells, _ := m["spells"].(map[string]any)
		lists, _ := spells["spell_list"].([]any)
		for _, l := range lists {
			lm, _ := l.(map[string]any)
			entries, _ := lm["spells"].([]any)
			for _, sp := range entries {
				spm, _ := sp.(map[string]any)
				n, _ := spm["name"].(string)
				if !strings.EqualFold(n, name) {
					continue
				}
				levelText, _ := lm["level_text"].(string)
				lt := strings.ToLower(levelText)
				if strings.Contains(lt, "cantrip") || strings.Contains(lt, "(") {
					return 0, true, true
				}
				lv, _ := toFloat64(lm["level"])
				return int(lv), false, true
			}
		}
	}
	return 0, false, false
}

// spellTraitNames extracts trait names from a spell doc's traits array.
func spellTraitNames(raw any) []string {
	arr, _ := raw.([]any)
	var names []string
	for _, t := range arr {
		if m, ok := t.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

func containsFold(names []string, want string) bool {
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return true
		}
	}
	return false
}

// escapeFilterName escapes a spell name for a JSONPath name filter.
func escapeFilterName(name string) string {
	name = strings.ReplaceAll(name, `\`, `\\`)
	return strings.ReplaceAll(name, `'`, `\'`)
}
