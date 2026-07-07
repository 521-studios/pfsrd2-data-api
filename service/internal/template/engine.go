package template

import (
	"encoding/json"
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
	// Group effects by target+operation to identify conditional chains.
	// Effects sharing a target AND operation form a first-match chain.
	// Different operations on the same target run as separate sequential steps.
	type targetGroup struct {
		target  string
		effects []Effect
	}

	var groups []targetGroup
	seen := map[string]int{} // "target\x00operation" → index in groups

	for _, eff := range change.Effects {
		key := eff.Target + "\x00" + eff.Operation
		if idx, ok := seen[key]; ok {
			groups[idx].effects = append(groups[idx].effects, eff)
		} else {
			seen[key] = len(groups)
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
		if m := eff.ItemMap(); m != nil {
			cp := make(map[string]any)
			for k, v := range m {
				cp[k] = v
			}
			cp["value"] = computed
			eff.Item = cp
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

// applyAddItems appends items from a source path to the target array.
// The source is typically "$.monster_template.changes[*].abilities" — collecting
// abilities from all changes in the template.
// Routes abilities to the correct container: special_senses get only known senses,
// other containers get non-sense abilities. Deduplicates by name (case-insensitive).
func applyAddItems(statBlock map[string]any, eff Effect, allChanges []Change) error {
	// Resolve target in stat_block, creating if missing
	resolved, err := resolvePath(statBlock, eff.Target)
	if err != nil {
		return fmt.Errorf("add_items resolve target: %w", err)
	}
	if len(resolved) == 0 {
		resolved = resolveOrCreate(statBlock, eff.Target)
	}

	// Collect items from the source, routing by container type
	var items []any
	isSpecialSenses := strings.HasSuffix(eff.Target, "special_senses")
	for _, c := range allChanges {
		for _, a := range c.Abilities {
			name := abilityName(a)
			isSense := isKnownSpecialSense(name)
			if isSpecialSenses && isSense {
				items = append(items, abilityToSpecialSense(a))
			} else if !isSpecialSenses && !isSense {
				items = append(items, deepCopy(a))
			}
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
		for _, item := range items {
			name := abilityName(item)
			if name == "" || !arrayContainsName(arr, name) {
				arr = append(arr, item)
			}
		}
		rv.Set(arr)
	}
	return nil
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

// applyReplaceOneDie implements the elemental damage conversion rule:
// "If the creature's Strikes deal more than one die of damage, change one die
// to <type> damage. If not, add 1 <type> damage to its Strikes."
//
// Per damage array (one Strike): considering only non-persistent entries with
// a parseable dice formula that aren't already the target type —
//   - if an entry has 2+ dice, split one die off it (the flat modifier stays
//     with the original entry) and insert the new-type die right after it
//   - else if there are 2+ single-die entries, convert the last one's type
//   - else if there is exactly one die total, append a flat 1 damage entry
//   - no parseable dice (effect-only strikes) → no change
func applyReplaceOneDie(rv ResolvedValue, eff Effect) error {
	newType, ok := eff.Value.(string)
	if !ok {
		return fmt.Errorf("replace_one_die: value must be a string, got %T", eff.Value)
	}
	arr, ok := rv.Get().([]any)
	if !ok || len(arr) == 0 {
		return nil
	}

	type cand struct {
		idx        int
		m          map[string]any
		count, die int
		mod        string
	}
	var cands []cand
	total := 0
	for i, elem := range arr {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		if p, _ := m["persistent"].(bool); p {
			continue
		}
		if dt, _ := m["damage_type"].(string); dt == newType {
			continue
		}
		f, _ := m["formula"].(string)
		g := diceFormula.FindStringSubmatch(f)
		if g == nil {
			continue
		}
		n, _ := strconv.Atoi(g[1])
		die, _ := strconv.Atoi(g[2])
		cands = append(cands, cand{idx: i, m: m, count: n, die: die, mod: g[3]})
		total += n
	}
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
	// 2+ dice but every candidate is a single die: convert the last one.
	cands[len(cands)-1].m["damage_type"] = newType
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
