package template

import (
	"fmt"
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

// splitPath breaks "foo.bar[*].baz.qux" into ["foo", "bar", "[*]", "baz", "qux"].
func splitPath(path string) []string {
	var segments []string
	for _, part := range strings.Split(path, ".") {
		if idx := strings.Index(part, "[*]"); idx >= 0 {
			if idx > 0 {
				segments = append(segments, part[:idx])
			}
			segments = append(segments, "[*]")
			rest := part[idx+3:]
			if rest != "" {
				segments = append(segments, strings.TrimPrefix(rest, "."))
			}
		} else {
			segments = append(segments, part)
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

	if seg == "[*]" {
		arr, ok := current.([]any)
		if !ok {
			// Not an array — skip silently (the field might not exist on this element)
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
