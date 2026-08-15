package template

import "github.com/wI2L/jsondiff"

// DiffPatch produces the RFC 6902 patch between two JSON snapshots as a single
// PatchGroup, using the same differ the template engine uses internally. It lets
// non-engine mutations (item-apply of a material or spell) return the same patch
// shape as a rune application, so consumers get one uniform contract. Returns an
// empty slice when nothing changed.
func DiffPatch(before, after []byte, category, description string) ([]PatchGroup, error) {
	patch, err := jsondiff.CompareJSON(before, after)
	if err != nil {
		return nil, err
	}
	if len(patch) == 0 {
		return []PatchGroup{}, nil
	}
	ops := make([]Operation, len(patch))
	for i, op := range patch {
		ops[i] = Operation{Op: op.Type, Path: op.Path, Value: op.Value}
	}
	return []PatchGroup{{ChangeCategory: category, Description: description, Operations: ops}}, nil
}
