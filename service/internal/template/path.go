package template

import (
	"fmt"
	"strconv"
	"strings"
)

// ResolvedValue points to a single value within a JSON tree so the engine can
// read and mutate it in place.
type ResolvedValue struct {
	// Parent is the map or slice containing the value.
	Parent any
	// Key is the map key (string) or slice index (int) within Parent.
	Key any
	// ArrayIndex tracks the outer [*] element index (-1 if not from a wildcard).
	ArrayIndex int
}

// Get returns the current value.
func (rv ResolvedValue) Get() any {
	switch p := rv.Parent.(type) {
	case map[string]any:
		return p[rv.Key.(string)]
	case []any:
		return p[rv.Key.(int)]
	}
	return nil
}

// Set writes a new value.
func (rv ResolvedValue) Set(v any) {
	switch p := rv.Parent.(type) {
	case map[string]any:
		p[rv.Key.(string)] = v
	case []any:
		p[rv.Key.(int)] = v
	}
}

// resolvePath resolves a limited JSONPath expression against stat_block.
//
// Supported syntax:
//
//	$.foo.bar.baz      → direct nested map traversal
//	$.foo[*].bar.baz   → iterate array elements, then descend
//	$.foo[*]           → each element of the array (used for bonuses arrays)
//
// The path is relative to stat_block ($ = stat_block root).
func resolvePath(statBlock map[string]any, path string) ([]ResolvedValue, error) {
	path = strings.TrimPrefix(path, "$.")
	if path == "$" || path == "" {
		return nil, fmt.Errorf("empty path")
	}

	segments := splitPath(path)
	return resolveSegments(statBlock, segments, -1)
}

// splitPath breaks a dotted path into segments, respecting bracket expressions.
//
//	"foo.bar[*].baz"                       → ["foo", "bar", "[*]", "baz"]
//	"foo[?(@.key=='val')].baz"             → ["foo", "[?(@.key=='val')]", "baz"]
//	"foo[?(@.movement_type=='walk')].value" → ["foo", "[?(@.movement_type=='walk')]", "value"]
func splitPath(path string) []string {
	var segments []string
	i := 0
	for i < len(path) {
		if path[i] == '[' {
			// Find matching closing bracket
			end := strings.Index(path[i:], "]")
			if end < 0 {
				segments = append(segments, path[i:])
				break
			}
			bracket := path[i : i+end+1]
			segments = append(segments, bracket)
			i += end + 1
			if i < len(path) && path[i] == '.' {
				i++ // skip dot after bracket
			}
		} else {
			// Find next dot or bracket
			end := i
			for end < len(path) && path[end] != '.' && path[end] != '[' {
				end++
			}
			if end > i {
				segments = append(segments, path[i:end])
			}
			i = end
			if i < len(path) && path[i] == '.' {
				i++ // skip dot
			}
		}
	}
	return segments
}

// resolveSegments recursively walks the path segments, expanding [*] wildcards.
func resolveSegments(current any, segments []string, arrayIdx int) ([]ResolvedValue, error) {
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments to resolve")
	}

	seg := segments[0]
	rest := segments[1:]

	// Filter expression: [?(@.field=='value')] or [?(@ == 'value')]
	if strings.HasPrefix(seg, "[?(") && strings.HasSuffix(seg, ")]") {
		arr, ok := current.([]any)
		if !ok {
			return nil, nil
		}
		var results []ResolvedValue
		for i, elem := range arr {
			if matchesFilter(elem, seg) {
				if len(rest) == 0 {
					results = append(results, ResolvedValue{Parent: arr, Key: i, ArrayIndex: i})
				} else {
					sub, err := resolveSegments(elem, rest, i)
					if err != nil {
						return nil, err
					}
					results = append(results, sub...)
				}
			}
		}
		return results, nil
	}

	if seg == "[*]" {
		arr, ok := current.([]any)
		if !ok {
			return nil, nil
		}
		var results []ResolvedValue
		for i := range arr {
			if len(rest) == 0 {
				results = append(results, ResolvedValue{Parent: arr, Key: i, ArrayIndex: i})
			} else {
				sub, err := resolveSegments(arr[i], rest, i)
				if err != nil {
					return nil, err
				}
				results = append(results, sub...)
			}
		}
		return results, nil
	}

	m, ok := current.(map[string]any)
	if !ok {
		return nil, nil
	}

	val, exists := m[seg]
	if !exists {
		return nil, nil
	}

	if len(rest) == 0 {
		return []ResolvedValue{{Parent: m, Key: seg, ArrayIndex: arrayIdx}}, nil
	}

	return resolveSegments(val, rest, arrayIdx)
}

// resolveOrCreate resolves a path, creating missing leaf fields as empty arrays.
// Used by add_item/add_items when the target doesn't exist yet (e.g., adding
// immunities to a creature that has none).
func resolveOrCreate(statBlock map[string]any, path string) []ResolvedValue {
	path = strings.TrimPrefix(path, "$.")
	segments := splitPath(path)
	if len(segments) == 0 {
		return nil
	}

	// Walk to the parent, expanding wildcards
	return createAtLeaf(statBlock, segments, -1)
}

// createAtLeaf walks segments, creating the final segment as an empty array
// if it doesn't exist. Handles [*] by iterating array elements.
func createAtLeaf(current any, segments []string, arrayIdx int) []ResolvedValue {
	if len(segments) == 1 {
		seg := segments[0]
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		if _, exists := m[seg]; !exists {
			m[seg] = []any{}
		}
		return []ResolvedValue{{Parent: m, Key: seg, ArrayIndex: arrayIdx}}
	}

	seg := segments[0]
	rest := segments[1:]

	// Handle filter expressions
	if strings.HasPrefix(seg, "[?(") && strings.HasSuffix(seg, ")]") {
		arr, ok := current.([]any)
		if !ok {
			return nil
		}
		var results []ResolvedValue
		for i, elem := range arr {
			if matchesFilter(elem, seg) {
				results = append(results, createAtLeaf(elem, rest, i)...)
			}
		}
		return results
	}

	if seg == "[*]" {
		arr, ok := current.([]any)
		if !ok {
			return nil
		}
		var results []ResolvedValue
		for i := range arr {
			results = append(results, createAtLeaf(arr[i], rest, i)...)
		}
		return results
	}

	m, ok := current.(map[string]any)
	if !ok {
		return nil
	}
	val, exists := m[seg]
	if !exists {
		return nil
	}
	return createAtLeaf(val, rest, arrayIdx)
}

// matchesFilter evaluates a JSONPath filter expression against an array element.
// Supports: [?(@.field=='value')], [?(@ == 'value')]
func matchesFilter(elem any, filter string) bool {
	if len(filter) < 6 { // minimum: [?(@)]
		return false
	}
	// Strip [?( and )]
	inner := filter[3 : len(filter)-2]
	inner = strings.TrimSpace(inner)

	// Parse: @ == 'value' (string element match)
	if strings.HasPrefix(inner, "@ ==") || strings.HasPrefix(inner, "@==") {
		idx := strings.Index(inner, "==")
		val := extractFilterValue(inner[idx+2:])
		s, ok := elem.(string)
		return ok && s == val
	}

	// Parse: @.field == 'value'
	if strings.HasPrefix(inner, "@.") {
		// Find operator
		for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
			idx := strings.Index(inner, op)
			if idx < 0 {
				continue
			}
			fieldPath := strings.TrimSpace(inner[2:idx]) // after "@."
			val := extractFilterValue(inner[idx+len(op):])

			// Resolve the field from the element
			elemVal := resolveFilterField(elem, fieldPath)

			switch op {
			case "==":
				return filterEquals(elemVal, val)
			case "!=":
				return !filterEquals(elemVal, val)
			case ">":
				return filterCompare(elemVal, val) > 0
			case "<":
				return filterCompare(elemVal, val) < 0
			case ">=":
				return filterCompare(elemVal, val) >= 0
			case "<=":
				return filterCompare(elemVal, val) <= 0
			}
		}
	}

	return false
}

// extractFilterValue extracts the value from a filter expression like " 'walk'" or " 20".
func extractFilterValue(s string) string {
	s = strings.TrimSpace(s)
	// String literal: 'value' or "value"
	if (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) ||
		(strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) {
		return s[1 : len(s)-1]
	}
	return s
}

// resolveFilterField resolves a dotted field path from an element.
// e.g., "movement_type" or "attack.weapon"
func resolveFilterField(elem any, fieldPath string) any {
	current := elem
	for _, part := range strings.Split(fieldPath, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}

// filterEquals compares a resolved value with a filter value string.
func filterEquals(actual any, expected string) bool {
	switch v := actual.(type) {
	case string:
		return v == expected
	case float64:
		if f, err := strconv.ParseFloat(expected, 64); err == nil {
			return v == f
		}
	}
	return false
}

// filterCompare compares numeric values. Returns -1, 0, or 1.
func filterCompare(actual any, expected string) int {
	a, aOk := toFloat64(actual)
	b, bOk := toFloat64(parseRHS(expected))
	if !aOk || !bOk {
		return 0
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// resolveScalar resolves a path and returns the first scalar value found.
// Used by the conditional evaluator to read a single value for comparison.
func resolveScalar(statBlock map[string]any, path string) (any, bool) {
	results, err := resolvePath(statBlock, path)
	if err != nil || len(results) == 0 {
		return nil, false
	}
	return results[0].Get(), true
}

// toJSONPointer converts a dotted path (from template effects) into a JSON
// Pointer (RFC 6901) for use in patch operations.
// Example: "defense.ac.value" → "/stat_block/defense/ac/value"
func toJSONPointer(dotPath string) string {
	dotPath = strings.TrimPrefix(dotPath, "$.")
	parts := strings.Split(dotPath, ".")
	// Filter out [*] markers — they become array indices in the actual diff
	var clean []string
	for _, p := range parts {
		p = strings.ReplaceAll(p, "[*]", "")
		if p != "" {
			clean = append(clean, p)
		}
	}
	return "/stat_block/" + strings.Join(clean, "/")
}
