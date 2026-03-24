package template

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
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

	for _, change := range tmpl.MonsterTemplate.Changes {
		// Snapshot before this change category
		beforeBytes, err := json.Marshal(working)
		if err != nil {
			return nil, fmt.Errorf("marshal before snapshot: %w", err)
		}

		if err := applyChange(statBlock, change, tmpl.MonsterTemplate.Changes); err != nil {
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

// applyChange applies all effects within a single Change to the stat_block.
// allChanges is passed for add_items operations that reference abilities across changes.
func applyChange(statBlock map[string]any, change Change, allChanges []Change) error {
	// Group effects by target to identify conditional chains.
	// Effects sharing a target form a first-match chain.
	type targetGroup struct {
		target  string
		effects []Effect
	}

	var groups []targetGroup
	seen := map[string]int{} // target → index in groups

	for _, eff := range change.Effects {
		if idx, ok := seen[eff.Target]; ok {
			groups[idx].effects = append(groups[idx].effects, eff)
		} else {
			seen[eff.Target] = len(groups)
			groups = append(groups, targetGroup{target: eff.Target, effects: []Effect{eff}})
		}
	}

	for _, g := range groups {
		// add_items is special — it needs access to all changes' abilities
		if len(g.effects) == 1 && g.effects[0].Operation == "add_items" {
			if err := applyAddItems(statBlock, g.effects[0], allChanges); err != nil {
				return err
			}
			continue
		}
		if err := applyEffectGroup(statBlock, g.effects); err != nil {
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
	// add_items is routed separately through applyAddItems in applyChange.
	if len(resolved) == 0 && len(effects) > 0 && effects[0].Operation == "add_item" {
		resolved = resolveOrCreate(statBlock, target)
	}

	accumulating := isAccumulatingOp(effects[0].Operation)

	for _, rv := range resolved {
		for _, eff := range effects {
			if evaluateConditional(statBlock, eff.Conditional, rv.ArrayIndex) {
				if err := applyOperation(rv, eff); err != nil {
					return err
				}
				if !accumulating {
					break // first match wins for non-accumulating ops
				}
			}
		}
	}
	return nil
}

// applySingleEffect resolves the target and applies the operation.
func applySingleEffect(statBlock map[string]any, eff Effect, arrayIdx int) error {
	// Resolve computed values before applying
	if eff.ValueFrom != "" {
		computed, err := evaluateValueFrom(statBlock, eff.ValueFrom, eff.Minimum)
		if err != nil {
			// Non-fatal: creature may lack the field (e.g., no walk speed for swim-only creatures)
			slog.Debug("value_from resolution skipped", "expr", eff.ValueFrom, "err", err)
			return nil
		}
		if eff.Item != nil {
			eff.Item["value"] = computed
		} else {
			eff.Value = computed
		}
	}

	resolved, err := resolvePath(statBlock, eff.Target)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", eff.Target, err)
	}

	// For add_item/add_items on missing fields, create the target array
	if len(resolved) == 0 && (eff.Operation == "add_item" || eff.Operation == "add_items") {
		resolved = resolveOrCreate(statBlock, eff.Target)
	}

	for _, rv := range resolved {
		if err := applyOperation(rv, eff); err != nil {
			return err
		}
	}
	return nil
}

// applyOperation performs the actual mutation on a resolved value.
func applyOperation(rv ResolvedValue, eff Effect) error {
	switch eff.Operation {
	case "adjustment":
		return applyAdjustment(rv, eff)
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
func applyAdjustment(rv ResolvedValue, eff Effect) error {
	adj, ok := toFloat64(eff.Value)
	if !ok {
		return fmt.Errorf("adjustment value is not numeric: %v", eff.Value)
	}

	current := rv.Get()
	switch val := current.(type) {
	case float64:
		rv.Set(val + adj)
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

// applyAddItems appends items from a source path to the target array.
// The source is typically "$.monster_template.changes[*].abilities" — collecting
// abilities from all changes in the template.
func applyAddItems(statBlock map[string]any, eff Effect, allChanges []Change) error {
	// Resolve target in stat_block, creating if missing
	resolved, err := resolvePath(statBlock, eff.Target)
	if err != nil {
		return fmt.Errorf("add_items resolve target: %w", err)
	}
	if len(resolved) == 0 {
		resolved = resolveOrCreate(statBlock, eff.Target)
	}

	// Collect items from the source — currently only
	// "$.monster_template.changes[*].abilities" is used
	var items []any
	for _, c := range allChanges {
		for _, a := range c.Abilities {
			items = append(items, a)
		}
	}

	if len(items) == 0 {
		return nil
	}

	for _, rv := range resolved {
		current := rv.Get()
		arr, ok := current.([]any)
		if !ok {
			arr = []any{}
		}
		arr = append(arr, items...)
		rv.Set(arr)
	}
	return nil
}

// applyAddItem appends an item to the target array.
// Two forms:
//   - eff.Item is set: append the item object directly
//   - eff.Name is set and target is a string array: append the name string
//   - eff.Name is set and target is an object array: append {name: eff.Name}
func applyAddItem(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		arr = []any{}
	}

	if eff.Item != nil {
		item := make(map[string]any)
		for k, v := range eff.Item {
			item[k] = v
		}
		arr = append(arr, item)
	} else if eff.Name != "" {
		// Determine if target is a string array or object array.
		// creature_types is a string array; most other arrays hold objects.
		if len(arr) > 0 {
			if _, isStr := arr[0].(string); isStr {
				arr = append(arr, eff.Name)
			} else {
				arr = append(arr, map[string]any{"name": eff.Name})
			}
		} else {
			// Empty array — default to string (creature_types is the common
			// case for name-based add_item on empty arrays; object arrays
			// typically use the item field instead).
			arr = append(arr, eff.Name)
		}
	} else {
		return fmt.Errorf("add_item requires item or name")
	}

	rv.Set(arr)
	return nil
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

	if maxIdx >= 0 && eff.Item != nil {
		// Replace the highest speed with the new item, preserving the value
		newItem := make(map[string]any)
		for k, v := range eff.Item {
			newItem[k] = v
		}
		newItem["value"] = maxVal
		arr[maxIdx] = newItem
		rv.Set(arr)
	}

	return nil
}

// applyReplaceOneDie changes one damage die type in a formula string.
// Used by Fire elemental template to change one damage die to fire.
func applyReplaceOneDie(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		return nil
	}
	if len(arr) == 0 {
		return nil
	}

	// Replace the damage type of the first damage entry
	if m, ok := arr[0].(map[string]any); ok {
		if newType, ok := eff.Value.(string); ok {
			m["damage_type"] = newType
		}
	}
	return nil
}

// applySetReach sets the reach value on attacks.
// Used by Miniature template.
func applySetReach(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	m, ok := current.(map[string]any)
	if !ok {
		return nil
	}
	m["reach"] = eff.Value
	rv.Set(m)
	return nil
}

func containsWildcard(path string) bool {
	return strings.Contains(path, "[*]")
}
