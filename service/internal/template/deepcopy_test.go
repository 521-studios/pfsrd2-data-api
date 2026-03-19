package template

import "testing"

func TestDeepCopy_Map(t *testing.T) {
	orig := map[string]any{
		"a": float64(1),
		"b": map[string]any{"c": float64(2)},
	}
	cp := deepCopy(orig).(map[string]any)

	// Mutate the copy
	cp["a"] = float64(99)
	cp["b"].(map[string]any)["c"] = float64(99)

	// Original should be unchanged
	if orig["a"] != float64(1) {
		t.Errorf("original map was mutated: a = %v", orig["a"])
	}
	if orig["b"].(map[string]any)["c"] != float64(2) {
		t.Errorf("original nested map was mutated: b.c = %v", orig["b"].(map[string]any)["c"])
	}
}

func TestDeepCopy_Slice(t *testing.T) {
	orig := []any{float64(1), float64(2), map[string]any{"x": float64(3)}}
	cp := deepCopy(orig).([]any)

	cp[0] = float64(99)
	cp[2].(map[string]any)["x"] = float64(99)

	if orig[0] != float64(1) {
		t.Errorf("original slice was mutated: [0] = %v", orig[0])
	}
	if orig[2].(map[string]any)["x"] != float64(3) {
		t.Errorf("original nested map in slice was mutated")
	}
}

func TestDeepCopy_Scalar(t *testing.T) {
	if deepCopy(float64(42)) != float64(42) {
		t.Error("float64 not passed through")
	}
	if deepCopy("hello") != "hello" {
		t.Error("string not passed through")
	}
	if deepCopy(nil) != nil {
		t.Error("nil not passed through")
	}
}
