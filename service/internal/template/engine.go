package template

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/wI2L/jsondiff"
)

// Apply applies a monster template to a creature stat block, returning grouped
// RFC 6902 patches and the modified result.
//
// The creature parameter should be the full creature JSON (with stat_block).
// The tmpl parameter is the parsed template JSON.
func Apply(creature map[string]any, tmpl TemplateJSON) (*ApplyResult, error) {
	working := deepCopy(creature).(map[string]any)

	statBlock, ok := working["stat_block"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("creature has no stat_block")
	}

	var patches []PatchGroup

	changes, pool := effectiveChanges(tmpl.MonsterTemplate)

	for _, change := range changes {
		// Snapshot before this change category
		beforeBytes, err := json.Marshal(working)
		if err != nil {
			return nil, fmt.Errorf("marshal before snapshot: %w", err)
		}

		if err := applyChange(statBlock, change, pool); err != nil {
			return nil, fmt.Errorf("apply change %q: %w", change.ChangeCategory, err)
		}

		// Diff to produce RFC 6902 operations
		afterBytes, err := json.Marshal(working)
		if err != nil {
			return nil, fmt.Errorf("marshal after snapshot: %w", err)
		}

		patch, err := jsondiff.CompareJSON(beforeBytes, afterBytes)
		if err != nil {
			return nil, fmt.Errorf("diff change %q: %w", change.ChangeCategory, err)
		}

		if len(patch) > 0 {
			ops := make([]Operation, len(patch))
			for i, op := range patch {
				ops[i] = Operation{
					Op:    op.Type,
					Path:  op.Path,
					Value: op.Value,
				}
			}
			patches = append(patches, PatchGroup{
				ChangeCategory: change.ChangeCategory,
				Description:    change.Text,
				Operations:     ops,
			})
		}
	}

	// Collect select operations as selections for the client
	var selections []any
	for _, change := range tmpl.MonsterTemplate.Changes {
		for _, eff := range change.Effects {
			if eff.Operation == "select" && eff.Selection != nil {
				sel := map[string]any{
					"change_category": change.ChangeCategory,
					"target":          eff.Target,
					"selection":       eff.Selection,
				}
				if eff.Conditional != "" {
					sel["conditional"] = eff.Conditional
				}
				selections = append(selections, sel)
			}
		}
	}
	if selections == nil {
		selections = []any{}
	}

	return &ApplyResult{
		PatchDoc: PatchDocument{
			AppliedPatches: patches,
			Selections:     selections,
		},
		Creature: working,
	}, nil
}

// effectiveChanges returns the changes to apply and the ability pool they
// draw from. Ability-only templates (Twinned, Tengu, the mythic roles) carry
// no changes array — they get a synthesized one routing each ability by
// category; templates with changes get a warning for any pool ability no
// effect consumes.
func effectiveChanges(mt MonsterTemplate) ([]Change, []any) {
	pool := abilityPool(mt)
	if len(mt.Changes) == 0 && len(pool) > 0 {
		return []Change{synthesizeAbilitiesChange(mt.Abilities)}, pool
	}
	warnUnconsumedAbilities(mt.Changes, pool)
	return mt.Changes, pool
}

// abilityPool collects every ability a template defines, whether on a change
// or at the monster_template level — add_items sources resolve against it.
func abilityPool(mt MonsterTemplate) []any {
	var pool []any
	for _, c := range mt.Changes {
		pool = append(pool, c.Abilities...)
	}
	pool = append(pool, mt.Abilities...)
	return pool
}

// abilityCategoryTargets routes ability_category values to the stat_block
// container each category's abilities live in on real creatures.
var abilityCategoryTargets = map[string]string{
	"special_sense": "$.senses.special_senses",
	"hp_automatic":  "$.defense.hitpoints[*].automatic_abilities",
	"automatic":     "$.defense.automatic_abilities",
	"reactive":      "$.defense.reactive_abilities",
	"interaction":   "$.interaction_abilities",
	"offensive":     "$.offense.offensive_actions",
}

// synthesizeAbilitiesChange builds the implicit change for ability-only
// templates: one add_items effect per ability, routed by its
// ability_category. The ability travels on the effect's Item field — no
// string round-trip, so names with quotes are safe.
func synthesizeAbilitiesChange(abilities []any) Change {
	change := Change{
		ChangeCategory: "abilities",
		Text:           "- Add the following abilities.",
		Abilities:      abilities,
	}
	for _, a := range abilities {
		change.Effects = append(change.Effects, Effect{
			Operation: "add_items",
			Item:      a,
			Target:    targetForAbility(a),
		})
	}
	return change
}

// targetForAbility routes one ability by its ability_category, falling back
// to the known-sense name check and finally to automatic_abilities (with a
// warning — a silent default would hide misrouted data).
func targetForAbility(a any) string {
	name := abilityName(a)
	m, ok := a.(map[string]any)
	if !ok {
		if isKnownSpecialSense(name) {
			return abilityCategoryTargets["special_sense"]
		}
		slog.Warn("ability has no ability_category, defaulting to automatic_abilities",
			"ability", name)
		return abilityCategoryTargets["automatic"]
	}
	category, _ := m["ability_category"].(string)
	if t, known := abilityCategoryTargets[category]; known {
		return t
	}
	if category != "" {
		slog.Warn("unknown ability_category, defaulting to automatic_abilities",
			"ability", name, "category", category)
		return abilityCategoryTargets["automatic"]
	}
	if isKnownSpecialSense(name) {
		return abilityCategoryTargets["special_sense"]
	}
	slog.Warn("ability has no ability_category, defaulting to automatic_abilities",
		"ability", name)
	return abilityCategoryTargets["automatic"]
}

// effectConsumption classifies what one add_items effect picks up from the
// ability pool: a single named ability (via Item or a name filter), every
// ability (unfiltered, non-sense target — senses divert, the rest append),
// or only the senses (unfiltered with a special_senses target, where
// non-sense abilities are not added). An unparseable filter consumes
// nothing — collectPoolItems rejects the same shape with an error.
func effectConsumption(eff Effect) (name string, all, sensesOnly bool) {
	if eff.Item != nil {
		return abilityName(eff.Item), false, false
	}
	if eff.Source == "" {
		return "", false, false
	}
	if strings.Contains(eff.Source, "[?(") {
		if m := sourceNameFilter.FindStringSubmatch(eff.Source); m != nil {
			return m[1], false, false
		}
		return "", false, false
	}
	if isSenseTarget(eff.Target) {
		return "", false, true
	}
	return "", true, false
}

// consumedFromChanges aggregates every add_items effect's consumption over a
// template's changes: the set of individually named abilities, whether any
// effect sweeps the whole pool, and whether any sweeps just the senses.
func consumedFromChanges(changes []Change) (names map[string]bool, all, senses bool) {
	names = map[string]bool{}
	for _, c := range changes {
		for _, eff := range c.Effects {
			if eff.Operation != "add_items" {
				continue
			}
			name, effAll, sensesOnly := effectConsumption(eff)
			switch {
			case name != "":
				names[strings.ToLower(name)] = true
			case effAll:
				all = true
			case sensesOnly:
				senses = true
			}
		}
	}
	return names, all, senses
}

// warnUnconsumedAbilities logs pool abilities that no add_items effect can
// pick up — not named by an Item-carrying or filtered effect, and not swept
// in by an unfiltered one. These are template-data gaps (or deliberate GM
// choice pools, like the Dark Archive cryptids) — the engine surfaces them
// rather than guessing.
func warnUnconsumedAbilities(changes []Change, pool []any) {
	consumed, all, sensesConsumed := consumedFromChanges(changes)
	if all {
		return
	}
	for _, a := range pool {
		name := abilityName(a)
		if name == "" || consumed[strings.ToLower(name)] {
			continue
		}
		if sensesConsumed && isSenseAbility(a) {
			continue
		}
		slog.Warn("template ability not referenced by any add_items effect", "ability", name)
	}
}

// groupEffects groups a change's effects by target+operation. Effects
// sharing a target AND operation form a first-match conditional chain;
// different operations on the same target run as separate sequential steps.
func groupEffects(effects []Effect) [][]Effect {
	var groups [][]Effect
	seen := map[string]int{} // "target\x00operation" → index in groups
	for _, eff := range effects {
		key := eff.Target + "\x00" + eff.Operation
		if idx, ok := seen[key]; ok {
			groups[idx] = append(groups[idx], eff)
		} else {
			seen[key] = len(groups)
			groups = append(groups, []Effect{eff})
		}
	}
	return groups
}

// applyChange applies all effects within a single Change to the stat_block.
// pool is the template's full ability collection for add_items sources.
func applyChange(statBlock map[string]any, change Change, pool []any) error {
	for _, effects := range groupEffects(change.Effects) {
		// add_items is special — it draws items from the template's ability
		// pool. Grouping is keyed by target+operation, so an add_items group
		// contains only add_items effects.
		if effects[0].Operation == "add_items" {
			for _, eff := range effects {
				if err := applyAddItems(statBlock, eff, pool); err != nil {
					ident := eff.Source
					if eff.Item != nil {
						ident = "ability " + abilityName(eff.Item)
					}
					return fmt.Errorf("add_items (%s, target %q): %w",
						ident, eff.Target, err)
				}
			}
			continue
		}
		if err := applyEffectGroup(statBlock, effects); err != nil {
			return err
		}
	}
	return nil
}

// isAccumulatingOp returns true for operations where multiple effects on the
// same target should all apply (not first-match-wins).
func isAccumulatingOp(op string) bool {
	return op == "add_item" || op == "remove_item" || op == "add_modifier"
}

// applyEffectGroup applies a group of effects sharing the same target.
// For accumulating ops (add_item, remove_item, add_modifier), all matching
// effects apply. For others (adjustment, replace), conditionals form a
// first-match chain.
func applyEffectGroup(statBlock map[string]any, effects []Effect) error {
	if len(effects) == 0 {
		return nil
	}

	target := effects[0].Target
	hasWildcard := containsWildcard(target)

	if hasWildcard {
		return applyWildcardEffects(statBlock, effects)
	}

	// Check if these effects accumulate or are first-match
	if isAccumulatingOp(effects[0].Operation) {
		for _, eff := range effects {
			if evaluateConditional(statBlock, eff.Conditional, -1) {
				if err := applySingleEffect(statBlock, eff, -1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// Non-accumulating: evaluate conditional chain, first match wins
	for _, eff := range effects {
		if evaluateConditional(statBlock, eff.Conditional, -1) {
			return applySingleEffect(statBlock, eff, -1)
		}
	}
	return nil
}

// applyWildcardEffects handles effects with [*] targets. For damage effects,
// the conditional is evaluated per-element (correlated wildcards).
func applyWildcardEffects(statBlock map[string]any, effects []Effect) error {
	target := effects[0].Target

	// Resolve the target to get all matching locations
	resolved, err := resolvePath(statBlock, target)
	if err != nil {
		return fmt.Errorf("resolve wildcard %q: %w", target, err)
	}

	// For add_item on missing fields within wildcard parents, create them.
	// Use conditional-aware creation when effects have non-trivial conditionals
	// to avoid creating empty arrays that break null-check conditionals.
	if len(resolved) == 0 && len(effects) > 0 && effects[0].Operation == "add_item" {
		hasConditional := false
		for _, eff := range effects {
			if eff.Conditional != "" && eff.Conditional != "default" {
				hasConditional = true
				break
			}
		}
		if hasConditional {
			resolved = resolveOrCreateConditional(statBlock, target, effects)
		} else {
			resolved = resolveOrCreate(statBlock, target)
		}
	}

	accumulating := isAccumulatingOp(effects[0].Operation)

	for _, rv := range resolved {
		for _, eff := range effects {
			if evaluateConditional(statBlock, eff.Conditional, rv.ArrayIndex) {
				if err := applyOperation(statBlock, rv, eff); err != nil {
					return err
				}
				if !accumulating {
					break // first match wins for non-accumulating ops
				}
			}
		}
	}

	// Clean up empty arrays that were created but never populated
	for _, rv := range resolved {
		if arr, ok := rv.Get().([]any); ok && len(arr) == 0 {
			if m, ok := rv.Parent.(map[string]any); ok {
				if key, ok := rv.Key.(string); ok {
					delete(m, key)
				}
			}
		}
	}

	return nil
}

// setMovementName rebuilds a speed item's display name around its computed
// value — "swim 15 feet", or bare "25 feet" for walk, matching how parsed
// creatures name their movement entries. No-op for non-speed items.
func setMovementName(item map[string]any, computed any) {
	mt, ok := item["movement_type"].(string)
	if !ok {
		return
	}
	v, ok := toFloat64(computed)
	if !ok {
		return
	}
	if mt == "walk" {
		item["name"] = fmt.Sprintf("%d feet", int(v))
	} else {
		item["name"] = fmt.Sprintf("%s %d feet", mt, int(v))
	}
}

// applySingleEffect resolves the target and applies the operation.
func applySingleEffect(statBlock map[string]any, eff Effect, arrayIdx int) error {
	// Resolve computed values before applying
	if eff.ValueFrom != "" {
		computed, err := evaluateValueFrom(statBlock, eff.ValueFrom, eff.Minimum)
		if errors.Is(err, ErrValueFromPath) {
			// Non-fatal: creature lacks the field (e.g., no walk speed for
			// swim-only creatures)
			slog.Debug("value_from resolution skipped", "expr", eff.ValueFrom, "err", err)
			return nil
		}
		if errors.Is(err, ErrValueFromShape) {
			// Template/schema mismatch: skip the effect but say so loudly —
			// this hides a template bug, not a creature quirk
			slog.Warn("value_from shape mismatch, effect skipped", "expr", eff.ValueFrom, "err", err)
			return nil
		}
		if err != nil {
			// Malformed template expression — surface it, don't skip
			return fmt.Errorf("value_from %q: %w", eff.ValueFrom, err)
		}
		if m := eff.ItemMap(); m != nil {
			cp := make(map[string]any)
			for k, v := range m {
				cp[k] = v
			}
			cp["value"] = computed
			setMovementName(cp, computed)
			eff.Item = cp
		} else {
			eff.Value = computed
		}
	}

	resolved, err := resolvePath(statBlock, eff.Target)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", eff.Target, err)
	}

	// For add_item on missing fields, create the target array
	// (add_items never reaches here — applyChange intercepts those groups)
	if len(resolved) == 0 && eff.Operation == "add_item" {
		resolved = resolveOrCreate(statBlock, eff.Target)
	}

	for _, rv := range resolved {
		if err := applyOperation(statBlock, rv, eff); err != nil {
			return err
		}
	}
	return nil
}

// applyOperation performs the actual mutation on a resolved value.
// statBlock is passed for operations that need to update ancestor text fields.
func applyOperation(statBlock map[string]any, rv ResolvedValue, eff Effect) error {
	switch eff.Operation {
	case "adjustment":
		return applyAdjustment(statBlock, rv, eff)
	case "add_modifier":
		return applyAddModifier(rv, eff)
	case "add_item":
		return applyAddItem(rv, eff)
	case "replace":
		return applyReplace(rv, eff)
	case "remove_item":
		return applyRemoveItem(rv, eff)
	case "replace_highest_with":
		return applyReplaceHighestWith(rv, eff)
	case "replace_one_die":
		return applyReplaceOneDie(rv, eff)
	case "set_reach":
		return applySetReach(rv, eff)
	case "remove_all_except":
		return applyRemoveAllExcept(rv, eff)
	case "size_increment":
		return nil // stub — not yet implemented
	case "select":
		return nil
	case "no_op":
		return nil
	default:
		return fmt.Errorf("unsupported operation: %s", eff.Operation)
	}
}

// applyAdjustment adds a numeric value to the resolved target.
// For arrays of numbers (like bonuses [29, 24, 19]), it adds to each element.
// After adjusting, updates sibling "text" fields that contain the old value.
func applyAdjustment(statBlock map[string]any, rv ResolvedValue, eff Effect) error {
	adj, ok := toFloat64(eff.Value)
	if !ok {
		return fmt.Errorf("adjustment value is not numeric: %v", eff.Value)
	}

	current := rv.Get()
	switch val := current.(type) {
	case float64:
		newVal := val + adj
		rv.Set(newVal)
		updateSiblingText(statBlock, eff.Target, rv, val, newVal)
	case []any:
		// Array of numbers (e.g., bonuses [29, 24, 19])
		for i, elem := range val {
			if num, ok := toFloat64(elem); ok {
				val[i] = num + adj
			}
		}
	default:
		return fmt.Errorf("cannot adjust non-numeric value: %T", current)
	}
	return nil
}

// updateSiblingText updates "text" fields in the parent map and all ancestor
// maps when a numeric value changes. Walks the statBlock using the target path
// to find all maps along the resolution chain and replaces the old value in
// their text fields.
func updateSiblingText(statBlock map[string]any, target string, rv ResolvedValue, oldVal, newVal float64) {
	if oldVal == newVal {
		return
	}

	oldStr := formatNum(oldVal)
	newStr := formatNum(newVal)
	pattern := regexp.MustCompile(`(^|[^\d])` + regexp.QuoteMeta(oldStr) + `($|[^\d])`)

	replaceText := func(m map[string]any) {
		for key, val := range m {
			if s, ok := val.(string); ok {
				updated := pattern.ReplaceAllString(s, "${1}"+newStr+"${2}")
				if updated != s {
					m[key] = updated
				}
			}
		}
	}

	// Walk the target path to find all ancestor maps (including the immediate
	// parent) and update their text fields.
	ancestors := collectAncestorMaps(statBlock, target, rv.Parent)
	for _, m := range ancestors {
		replaceText(m)
	}
}

// collectAncestorMaps walks a JSONPath and collects all map[string]any objects
// encountered along the way (excluding the leaf). Used to find ability objects
// that contain saving_throw objects whose dc was adjusted.
func collectAncestorMaps(statBlock map[string]any, path string, parent any) []map[string]any {
	cleanPath := strings.TrimPrefix(path, "$.")
	segments := splitPath(cleanPath)
	if len(segments) <= 1 {
		return nil
	}

	// Walk the path, and for wildcards search for the element that
	// eventually contains our target parent (by identity).
	var result []map[string]any
	walkAncestors(statBlock, segments[:len(segments)-1], parent, &result)
	return result
}

// walkAncestors recursively walks path segments collecting ancestor maps.
// For wildcards, searches each array element to find the one containing target.
func walkAncestors(current any, segments []string, target any, ancestors *[]map[string]any) bool {
	if len(segments) == 0 {
		// Reached the leaf parent — check identity via pointer comparison
		return sameMap(current, target)
	}

	seg := segments[0]
	rest := segments[1:]

	if seg == "[*]" {
		arr, ok := current.([]any)
		if !ok {
			return false
		}
		for _, elem := range arr {
			if m, ok := elem.(map[string]any); ok {
				*ancestors = append(*ancestors, m)
				if walkAncestors(elem, rest, target, ancestors) {
					return true
				}
				// Not on this branch — remove the added ancestor
				*ancestors = (*ancestors)[:len(*ancestors)-1]
			}
		}
		return false
	}

	if strings.HasPrefix(seg, "[?(") {
		return false
	}

	m, ok := current.(map[string]any)
	if !ok {
		return false
	}
	next := m[seg]
	if nextM, ok := next.(map[string]any); ok {
		*ancestors = append(*ancestors, nextM)
		if walkAncestors(next, rest, target, ancestors) {
			return true
		}
		*ancestors = (*ancestors)[:len(*ancestors)-1]
	} else {
		return walkAncestors(next, rest, target, ancestors)
	}
	return false
}

func formatNum(v float64) string {
	if v == math.Trunc(v) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// applyAddModifier appends a modifier object to the target array.
func applyAddModifier(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		// If target is nil or not an array, create a new array
		arr = []any{}
	}

	// Convert modifier map to any for JSON compatibility
	modifier := make(map[string]any)
	if eff.Modifier != nil {
		for k, v := range eff.Modifier {
			modifier[k] = v
		}
	}

	arr = append(arr, modifier)
	rv.Set(arr)
	return nil
}

// knownSpecialSenses is the set of ability names that belong in special_senses
// rather than automatic_abilities. Compared case-insensitively.
var knownSpecialSenses = map[string]bool{
	"darkvision":         true,
	"greater darkvision": true,
	"low-light vision":   true,
	"scent":              true,
	"tremorsense":        true,
	"wavesense":          true,
	"lifesense":          true,
	"all-around vision":  true,
	"echolocation":       true,
	"thoughtsense":       true,
	"motionsense":        true,
}

func isKnownSpecialSense(name string) bool {
	return knownSpecialSenses[strings.ToLower(name)]
}

func abilityName(a any) string {
	if s, ok := a.(string); ok {
		return s
	}
	if m, ok := a.(map[string]any); ok {
		if n, ok := m["name"].(string); ok {
			return n
		}
	}
	return ""
}

// abilityToSpecialSense converts an ability map to a special_sense map.
func abilityToSpecialSense(a any) any {
	m, ok := a.(map[string]any)
	if !ok {
		return a
	}
	ss := deepCopy(m).(map[string]any)
	ss["subtype"] = "special_sense"
	ss["type"] = "stat_block_section"
	delete(ss, "ability_type")
	return ss
}

// sourceNameFilter matches a source path's [?(@.name=='X')] filter; when
// FindStringSubmatch returns a match, the captured group is the ability name.
var sourceNameFilter = regexp.MustCompile(`\[\?\(@\.name=='([^']+)'\)\]`)

const specialSensesTarget = "$.senses.special_senses"

// isSenseTarget reports whether a target path is a special_senses container.
func isSenseTarget(target string) bool {
	return strings.HasSuffix(target, "special_senses")
}

// isSenseAbility reports whether an ability is a special sense — by its
// ability_category when present, else by the known-sense name list. Both
// signals matter: category-marked senses can have names outside the list
// (Spiritsense-style), and change-level abilities often lack a category.
func isSenseAbility(a any) bool {
	if m, ok := a.(map[string]any); ok {
		if category, _ := m["ability_category"].(string); category == "special_sense" {
			return true
		}
	}
	return isKnownSpecialSense(abilityName(a))
}

// itemForTarget prepares one ability for insertion at target: sense
// containers get the special_sense conversion, offensive_actions get the
// display wrapper, everything else a deep copy.
func itemForTarget(a any, target string) any {
	if isSenseTarget(target) {
		return abilityToSpecialSense(a)
	}
	if strings.HasSuffix(target, "offensive_actions") {
		return wrapOffensiveAbility(a)
	}
	return deepCopy(a)
}

// wrapOffensiveAbility wraps a plain ability in the offensive_action entry
// shape real creatures use ({name, ability, offensive_action_type}); the
// display layer renders wrappers, not bare abilities. Items already carrying
// a payload key (ability/attack/spells/mythic_ability) are wrappers and pass
// through unchanged.
func wrapOffensiveAbility(a any) any {
	m, ok := a.(map[string]any)
	if !ok {
		return deepCopy(a)
	}
	for _, k := range []string{"ability", "attack", "spells", "mythic_ability"} {
		if _, wrapped := m[k]; wrapped {
			return deepCopy(a)
		}
	}
	return map[string]any{
		"name":                  abilityName(a),
		"ability":               deepCopy(a),
		"offensive_action_type": "ability",
		"subtype":               "offensive_action",
		"type":                  "stat_block_section",
	}
}

// filteredPoolItems selects pool abilities matching a name filter. The
// explicit filter wins over routing heuristics, wherever the target points;
// zero matches is a template-data error. The result has a single key (the
// given target) — the map shape exists to match collectPoolItems' by-target
// contract.
func filteredPoolItems(pool []any, name, target string) (map[string][]any, error) {
	want := strings.ToLower(name)
	var items []any
	for _, a := range pool {
		if strings.ToLower(abilityName(a)) == want {
			items = append(items, itemForTarget(a, target))
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no template ability matches filter %q", name)
	}
	return map[string][]any{target: items}, nil
}

// unfilteredPoolItems routes the whole pool: senses are diverted to
// special_senses (real creatures keep Darkvision there, and NPC Core
// templates target automatic_abilities with an unfiltered source), other
// abilities go to the effect's target. A non-sense ability with a sense
// target is not added — a sense container only takes senses.
func unfilteredPoolItems(pool []any, target string) map[string][]any {
	byTarget := map[string][]any{}
	for _, a := range pool {
		switch {
		case isSenseAbility(a):
			byTarget[specialSensesTarget] = append(byTarget[specialSensesTarget], abilityToSpecialSense(a))
		case !isSenseTarget(target):
			byTarget[target] = append(byTarget[target], itemForTarget(a, target))
		}
	}
	return byTarget
}

// collectPoolItems selects the abilities an add_items effect adds, keyed by
// the target they belong on. Selection modes, in order: an ability carried
// on eff.Item (synthesized changes, already routed by category); a
// name-filtered source; an unfiltered source routing the whole pool.
func collectPoolItems(eff Effect, pool []any) (map[string][]any, error) {
	if eff.Item != nil {
		return map[string][]any{eff.Target: {itemForTarget(eff.Item, eff.Target)}}, nil
	}
	if eff.Source == "" {
		return nil, fmt.Errorf("add_items effect has neither item nor source")
	}
	if strings.Contains(eff.Source, "[?(") {
		m := sourceNameFilter.FindStringSubmatch(eff.Source)
		if m == nil {
			return nil, fmt.Errorf("unparseable filter in add_items source %q", eff.Source)
		}
		return filteredPoolItems(pool, m[1], eff.Target)
	}
	return unfilteredPoolItems(pool, eff.Target), nil
}

// applyAddItems appends the abilities selected by collectPoolItems to their
// containers, creating missing arrays and deduplicating by name
// (case-insensitive).
func applyAddItems(statBlock map[string]any, eff Effect, pool []any) error {
	byTarget, err := collectPoolItems(eff, pool)
	if err != nil {
		return err
	}
	for target, items := range byTarget {
		if err := appendItemsAt(statBlock, target, items); err != nil {
			return err
		}
	}
	return nil
}

// appendItemsAt appends items to the array(s) at target, creating the array
// when absent and skipping items whose name already exists there. Locations
// never share item maps — a [*] target (e.g. multi-headed creatures'
// hitpoints entries) gets an independent copy per location. A target that
// neither resolves nor can be created is a template-data error (a silent
// no-op here loses abilities, as zombie's Slow once did).
func appendItemsAt(statBlock map[string]any, target string, items []any) error {
	resolved, err := resolvePath(statBlock, target)
	if err != nil {
		return fmt.Errorf("resolve add_items target %q: %w", target, err)
	}
	if len(resolved) == 0 {
		resolved = resolveOrCreate(statBlock, target)
	}
	if len(resolved) == 0 {
		return fmt.Errorf("add_items target %q does not resolve and cannot be created", target)
	}

	for i, rv := range resolved {
		batch := items
		if i > 0 {
			batch = make([]any, len(items))
			for j, item := range items {
				batch[j] = deepCopy(item)
			}
		}
		appendMissing(rv, batch)
	}
	return nil
}

// appendMissing appends items whose names aren't already present in the
// array at rv, coercing a non-array value to a fresh array.
func appendMissing(rv ResolvedValue, items []any) {
	arr, ok := rv.Get().([]any)
	if !ok {
		arr = []any{}
	}
	for _, item := range items {
		name := abilityName(item)
		if name == "" || !arrayContainsName(arr, name) {
			arr = append(arr, item)
		}
	}
	rv.Set(arr)
}

// applyAddItem appends an item to the target array.
// Deduplicates by name (case-insensitive).
// Three forms:
//   - eff.Item is a map: append the item object directly
//   - eff.Item is a string: append the string directly (e.g., spell notes)
//   - eff.Name is set: append as string or {name: eff.Name} based on array type
func applyAddItem(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		arr = []any{}
	}

	// Determine the name of the item being added for dedup.
	// Prioritize ItemMap()["name"] since that's what actually gets appended.
	addName := ""
	if m := eff.ItemMap(); m != nil {
		if n, ok := m["name"].(string); ok {
			addName = n
		}
	}
	if addName == "" {
		addName = eff.Name
	}

	// Skip if an item with this name already exists (case-insensitive)
	if addName != "" && arrayContainsName(arr, addName) {
		return nil
	}

	if m := eff.ItemMap(); m != nil {
		item := deepCopy(m).(map[string]any)
		arr = append(arr, item)
	} else if s := eff.ItemString(); s != "" {
		// Item is a string (e.g., spell notes like "+4 damage (Elite, limited use)")
		arr = append(arr, s)
	} else if eff.Name != "" {
		// Determine if target is a string array or object array.
		if len(arr) > 0 {
			if _, isStr := arr[0].(string); isStr {
				arr = append(arr, eff.Name)
			} else {
				arr = append(arr, map[string]any{"name": eff.Name})
			}
		} else {
			arr = append(arr, eff.Name)
		}
	} else {
		return fmt.Errorf("add_item requires item or name")
	}

	rv.Set(arr)
	return nil
}

// arrayContainsName checks if an array already contains an item with the given name.
// Comparison is case-insensitive since data may use inconsistent casing.
func arrayContainsName(arr []any, name string) bool {
	lower := strings.ToLower(name)
	for _, elem := range arr {
		switch v := elem.(type) {
		case string:
			if strings.ToLower(v) == lower {
				return true
			}
		case map[string]any:
			if n, ok := v["name"].(string); ok && strings.ToLower(n) == lower {
				return true
			}
		}
	}
	return false
}

// applyReplace sets the target to the given value.
func applyReplace(rv ResolvedValue, eff Effect) error {
	rv.Set(eff.Value)
	return nil
}

// applyRemoveItem removes an item from an array by name.
// For string arrays, removes the matching string.
// For object arrays, removes objects where .name matches.
func applyRemoveItem(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		return nil // nothing to remove from
	}

	filtered := make([]any, 0, len(arr))
	for _, elem := range arr {
		switch v := elem.(type) {
		case string:
			if v != eff.Name {
				filtered = append(filtered, elem)
			}
		case map[string]any:
			if name, ok := v["name"].(string); !ok || name != eff.Name {
				filtered = append(filtered, elem)
			}
		default:
			filtered = append(filtered, elem)
		}
	}

	rv.Set(filtered)
	return nil
}

// applyRemoveAllExcept keeps only array items matching a field value and removes the rest.
// Currently supports matching by movement_type (used by Ghost template to keep only fly speed).
func applyRemoveAllExcept(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		return nil
	}

	if eff.MovementType == "" {
		return fmt.Errorf("remove_all_except: movement_type required")
	}

	filtered := make([]any, 0, len(arr))
	for _, elem := range arr {
		if m, ok := elem.(map[string]any); ok {
			if mt, ok := m["movement_type"].(string); ok && strings.EqualFold(mt, eff.MovementType) {
				filtered = append(filtered, elem)
			}
		}
	}

	rv.Set(filtered)
	return nil
}

// applyReplaceHighestWith replaces the highest-value speed with a new movement type.
// Used by Ghost template to replace the fastest speed with fly speed.
func applyReplaceHighestWith(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		return nil
	}

	// Find the element with the highest "value" field
	maxIdx := -1
	maxVal := math.Inf(-1)
	for i, elem := range arr {
		if m, ok := elem.(map[string]any); ok {
			if v, ok := toFloat64(m["value"]); ok && v > maxVal {
				maxVal = v
				maxIdx = i
			}
		}
	}

	if m := eff.ItemMap(); maxIdx >= 0 && m != nil {
		// Replace the highest speed with the new item, preserving the value
		newItem := deepCopy(m).(map[string]any)
		newItem["value"] = maxVal
		// Generate the name field (e.g., "fly 35 feet")
		if mt, ok := newItem["movement_type"].(string); ok {
			newItem["name"] = fmt.Sprintf("%s %d feet", mt, int(maxVal))
		}
		arr[maxIdx] = newItem
		rv.Set(arr)
	}

	return nil
}

// diceFormula matches "NdX" with an optional flat modifier, e.g. "2d8+9".
var diceFormula = regexp.MustCompile(`^(\d+)d(\d+)([+-]\d+)?$`)

// diceCand is a damage entry eligible for conversion to the target type.
type diceCand struct {
	idx        int
	m          map[string]any
	count, die int
	mod        string
}

// isRider reports whether a damage entry is a rider (persistent or splash)
// rather than part of the Strike's own damage dice.
func isRider(m map[string]any) bool {
	p, _ := m["persistent"].(bool)
	s, _ := m["splash"].(bool)
	return p || s
}

// scanDiceCandidates walks a damage array and returns the entries eligible for
// conversion to newType, plus the Strike's total die count for rule-branch
// selection. Rider entries are skipped entirely. Entries already of the target
// type count toward the total (the Strike does deal those dice) but are not
// candidates.
func scanDiceCandidates(arr []any, newType string) (cands []diceCand, total int) {
	for i, elem := range arr {
		m, ok := elem.(map[string]any)
		if !ok || isRider(m) {
			continue
		}
		f, _ := m["formula"].(string)
		g := diceFormula.FindStringSubmatch(f)
		if g == nil {
			continue
		}
		// Atoi cannot fail: diceFormula only captures digit runs, and real
		// formulas are far below the int range.
		n, _ := strconv.Atoi(g[1])
		total += n
		if dt, _ := m["damage_type"].(string); dt == newType {
			continue
		}
		die, _ := strconv.Atoi(g[2])
		cands = append(cands, diceCand{idx: i, m: m, count: n, die: die, mod: g[3]})
	}
	return cands, total
}

// applyReplaceOneDie implements the elemental damage conversion rule:
// "If the creature's Strikes deal more than one die of damage, change one die
// to <type> damage. If not, add 1 <type> damage to its Strikes."
//
// Per damage array (one Strike), with dice counted by scanDiceCandidates:
//   - 2+ dice: split one die off the first 2+ die candidate (the flat
//     modifier stays with the original entry, the new-type die is inserted
//     right after it), or convert the last single-die candidate; a Strike
//     whose dice are already all the target type is left unchanged
//   - exactly one die: append a flat 1 damage entry
//   - no dice (effect-only strikes) → no change
func applyReplaceOneDie(rv ResolvedValue, eff Effect) error {
	newType, ok := eff.Value.(string)
	if !ok {
		return fmt.Errorf("replace_one_die: value must be a string, got %T", eff.Value)
	}
	arr, ok := rv.Get().([]any)
	if !ok || len(arr) == 0 {
		return nil
	}

	cands, total := scanDiceCandidates(arr, newType)
	if total == 0 {
		return nil
	}

	newEntry := func(formula string) map[string]any {
		return map[string]any{
			"damage_type": newType,
			"formula":     formula,
			"subtype":     "attack_damage",
			"type":        "stat_block_section",
		}
	}

	if total == 1 {
		rv.Set(append(arr, newEntry("1")))
		return nil
	}
	for _, c := range cands {
		if c.count >= 2 {
			c.m["formula"] = fmt.Sprintf("%dd%d%s", c.count-1, c.die, c.mod)
			out := make([]any, 0, len(arr)+1)
			out = append(out, arr[:c.idx+1]...)
			out = append(out, newEntry(fmt.Sprintf("1d%d", c.die)))
			out = append(out, arr[c.idx+1:]...)
			rv.Set(out)
			return nil
		}
	}
	if len(cands) == 0 {
		return nil // 2+ dice but all already the target type
	}
	// 2+ dice but every candidate is a single die: convert the last one.
	cands[len(cands)-1].m["damage_type"] = newType
	return nil
}

// applySetReach reduces an attack's Reach trait to the effect's value in
// feet (Miniature semantics: notable-reach melee Strikes drop to 5 feet).
// Attacks without a Reach trait are untouched — their reach is implied by
// the creature's size, not stored on the attack. Only the trait's value is
// rewritten: its text is glossary rules prose, not a value display.
func applySetReach(rv ResolvedValue, eff Effect) error {
	v, ok := toFloat64(eff.Value)
	if !ok {
		return fmt.Errorf("set_reach: value must be numeric, got %T (%v)", eff.Value, eff.Value)
	}
	attack, ok := rv.Get().(map[string]any)
	if !ok {
		return nil
	}
	feet := fmt.Sprintf("%d feet", int(v))
	traits, _ := attack["traits"].([]any)
	for _, tr := range traits {
		m, ok := tr.(map[string]any)
		if !ok || m["name"] != "Reach" {
			continue
		}
		// set_reach only ever reduces: a Tiny creature's existing
		// "Reach 0 feet" must not be raised to 5.
		if old, okOld := m["value"].(string); okOld {
			var oldFeet int
			if _, err := fmt.Sscanf(old, "%d", &oldFeet); err == nil && oldFeet <= int(v) {
				continue
			}
		}
		m["value"] = feet
	}
	return nil
}

// sameMap checks if two values point to the same underlying map by pointer.
func sameMap(a, b any) bool {
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	if va.Kind() != reflect.Map || vb.Kind() != reflect.Map {
		return false
	}
	return va.Pointer() == vb.Pointer()
}

func containsWildcard(path string) bool {
	return strings.Contains(path, "[*]")
}
