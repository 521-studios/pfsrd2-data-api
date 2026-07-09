package template

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// SpellResolver fetches a full entry document by game_id. The API layer
// injects a DB+S3-backed implementation; tests inject fixtures. Spell swaps
// require it — without a resolver they are rejected as bad selections.
type SpellResolver func(gameID string) (map[string]any, error)

// ErrSpellNotFound marks a game_id with no entry — a caller mistake (400).
// Any other resolver error is infrastructure (DB/S3/parse) and surfaces as
// an internal error, never a client-blaming bad selection.
var ErrSpellNotFound = errors.New("spell not found")

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
		slots := findSpellSlots(statBlock, sw.From)
		if len(slots) == 0 {
			return nil, fmt.Errorf("%w: creature has no spell %q", ErrBadSelection, sw.From)
		}
		// The replace targets every list containing the name, so all
		// occurrences must agree on rank and cantrip-ness — a name at
		// two different ranks cannot satisfy same-rank for both.
		for _, sl := range slots[1:] {
			if sl.rank != slots[0].rank || sl.isCantrip != slots[0].isCantrip {
				return nil, fmt.Errorf("%w: %q appears at multiple ranks on this creature; swap is ambiguous",
					ErrBadSelection, sw.From)
			}
		}
		slotRank, slotIsCantrip := slots[0].rank, slots[0].isCantrip

		doc, err := resolve(sw.ReplacementGameID)
		if err != nil {
			if errors.Is(err, ErrSpellNotFound) {
				return nil, fmt.Errorf("%w: replacement spell %q not found", ErrBadSelection, sw.ReplacementGameID)
			}
			return nil, fmt.Errorf("resolve replacement spell %q: %w", sw.ReplacementGameID, err)
		}
		if doc == nil {
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
		value := map[string]any{
			"name":    lower,
			"subtype": "spell",
			"type":    "stat_block_section",
			"links": []any{map[string]any{
				"name": lower, "alt": lower, "aonid": doc["aonid"],
				"game-obj": "Spells", "type": "link",
			}},
		}
		// The slot's usage markers (at will, ×N) describe the slot, not
		// the spell — the replacement keeps them.
		for _, k := range []string{"count", "count_text"} {
			if v, ok := slots[0].entry[k]; ok {
				value[k] = v
			}
		}
		effects = append(effects, Effect{
			Operation: "replace",
			Target: fmt.Sprintf("%s[?(@.name=='%s')]",
				ref.eff.Target, escapeFilterName(strings.ToLower(sw.From))),
			Value: value,
		})
	}
	return effects, nil
}

// spellSlot is one occurrence of a spell name in the creature's lists.
type spellSlot struct {
	rank      int
	isCantrip bool
	entry     map[string]any
}

// findSpellSlots locates every occurrence of a spell name in the creature's
// spell lists. Cantrip groups are identified by the list's explicit
// cantrips flag — constant spell groups share the parenthesized level_text
// shape ("(5th)") but are ranked spells, so text parsing misclassifies them.
func findSpellSlots(statBlock map[string]any, name string) []spellSlot {
	var out []spellSlot
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
				isCantrip, _ := lm["cantrips"].(bool)
				lv, _ := toFloat64(lm["level"])
				out = append(out, spellSlot{rank: int(lv), isCantrip: isCantrip, entry: spm})
			}
		}
	}
	return out
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

// skillNameRe: letters, spaces, apostrophes, hyphens — covers standard
// skills and arbitrary Lores ("Legal Lore", "Sarkoris Lore").
var skillNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z' -]{0,60}$`)

// chosenSkillRe finds the templating token in pool ability text.
var chosenSkillRe = regexp.MustCompile(`(?i)the chosen skill`)

// poolWithSkillTemplated deep-copies the ability pool, substituting the
// chosen skill into any ability that references "the chosen skill" and
// suffixing its name so the pick is visible in the stat block
// (Official Bully (Legal Lore)). The returned renames map (old → new name)
// lets the caller rewrite name-filtered add_items sources to keep matching.
func poolWithSkillTemplated(pool []any, skill string) ([]any, map[string]string) {
	out := make([]any, len(pool))
	renames := map[string]string{}
	for i, a := range pool {
		out[i] = a
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		if !strings.Contains(strings.ToLower(text), "the chosen skill") {
			continue
		}
		cp := deepCopy(m).(map[string]any)
		cp["text"] = chosenSkillRe.ReplaceAllString(text, skill)
		if name, _ := cp["name"].(string); name != "" {
			newName := fmt.Sprintf("%s (%s)", name, skill)
			cp["name"] = newName
			renames[name] = newName
		}
		out[i] = cp
	}
	return out, renames
}
