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
// Some templates (NPC Core ancestries, War of Immortals mythic, Twinned)
// keep their ability definitions at this level rather than on a change.
type MonsterTemplate struct {
	Name        string   `json:"name"`
	Changes     []Change `json:"changes"`
	Abilities   []any    `json:"abilities,omitempty"`
	Adjustments []any    `json:"adjustments,omitempty"`
}

// Change groups related effects under a change_category with descriptive text.
type Change struct {
	Text           string   `json:"text"`
	ChangeCategory string   `json:"change_category"`
	Effects        []Effect `json:"effects"`
	Abilities      []any    `json:"abilities,omitempty"`
}

// Effect is a single conditional operation targeting a JSONPath within stat_block.
// The Item field can be either a map (object to add) or a string (text to add).
type Effect struct {
	Conditional  string         `json:"conditional,omitempty"`
	Target       string         `json:"target"`
	Operation    string         `json:"operation"`
	Value        any            `json:"value,omitempty"`
	Modifier     map[string]any `json:"modifier,omitempty"`
	Item         any            `json:"item,omitempty"`
	Name         string         `json:"name,omitempty"`
	Names        []string       `json:"names,omitempty"`
	Source       string         `json:"source,omitempty"`
	ValueFrom    string         `json:"value_from,omitempty"`
	Minimum      *float64       `json:"minimum,omitempty"`
	Selection    map[string]any `json:"selection,omitempty"`
	MovementType string         `json:"movement_type,omitempty"`
}

// ItemMap returns the Item as a map if it is one, or nil otherwise.
func (e Effect) ItemMap() map[string]any {
	if m, ok := e.Item.(map[string]any); ok {
		return m
	}
	return nil
}

// ItemString returns the Item as a string if it is one, or empty string otherwise.
func (e Effect) ItemString() string {
	if s, ok := e.Item.(string); ok {
		return s
	}
	return ""
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
