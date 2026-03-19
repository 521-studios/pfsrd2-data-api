// Package template applies monster template effects to creatures and produces
// RFC 6902 JSON Patch documents grouped by change category.
package template

// TemplateJSON mirrors the top-level monster template JSON structure.
// We only decode the fields the engine needs.
type TemplateJSON struct {
	Name            string          `json:"name"`
	MonsterTemplate MonsterTemplate `json:"monster_template"`
}

// MonsterTemplate is the nested object containing the changes array.
type MonsterTemplate struct {
	Name    string   `json:"name"`
	Changes []Change `json:"changes"`
}

// Change groups related effects under a change_category with descriptive text.
type Change struct {
	Text           string   `json:"text"`
	ChangeCategory string   `json:"change_category"`
	Effects        []Effect `json:"effects"`
}

// Effect is a single conditional operation targeting a JSONPath within stat_block.
type Effect struct {
	Conditional string         `json:"conditional,omitempty"`
	Target      string         `json:"target"`
	Operation   string         `json:"operation"`
	Value       any            `json:"value,omitempty"`
	Modifier    map[string]any `json:"modifier,omitempty"`
}

// ---------------------------------------------------------------------------
// Response types
// ---------------------------------------------------------------------------

// ApplyResult is returned by the Apply function. The API layer writes the
// PatchDoc and Creature as separate parts in a multipart MIME response.
type ApplyResult struct {
	PatchDoc PatchDocument
	Creature map[string]any
}

// PatchDocument is the first MIME part: grouped RFC 6902 patches + selections.
type PatchDocument struct {
	AppliedPatches []PatchGroup `json:"applied_patches"`
	Selections     []any        `json:"selections"`
}

// PatchGroup contains the RFC 6902 operations for one change category.
type PatchGroup struct {
	ChangeCategory string      `json:"change_category"`
	Description    string      `json:"description"`
	Operations     []Operation `json:"operations"`
}

// Operation is an RFC 6902 JSON Patch operation.
type Operation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}
