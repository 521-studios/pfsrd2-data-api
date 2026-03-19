package template

// deepCopy returns a deep copy of a JSON-decoded value (map, slice, or scalar).
func deepCopy(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, v := range val {
			out[k] = deepCopy(v)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, v := range val {
			out[i] = deepCopy(v)
		}
		return out
	default:
		return v
	}
}
