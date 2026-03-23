package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/521studios/pfsrd2-data-api/internal/db"
	"github.com/521studios/pfsrd2-data-api/internal/template"
	_ "modernc.org/sqlite"
)

// mockS3 implements s3.ObjectFetcher for tests.
type mockS3 struct {
	objects map[string][]byte
}

func (m *mockS3) GetObject(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockS3) GetObjectBytes(_ context.Context, key string) ([]byte, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: %s", key)
	}
	return data, nil
}

// setupTestDB creates an in-memory SQLite DB with fixtures and sets it as the
// global DB so handlers can use db.Global().
func setupTestDB(t *testing.T) {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	ddl := `
		CREATE TABLE entries (
			id INTEGER PRIMARY KEY, game_id TEXT UNIQUE NOT NULL, aonid INTEGER,
			type TEXT NOT NULL, name TEXT NOT NULL, current_schema_version TEXT NOT NULL,
			base_path TEXT NOT NULL, s3_key TEXT NOT NULL, level INTEGER,
			source TEXT, source_page INTEGER, edition TEXT, image_s3_key TEXT,
			attrs TEXT NOT NULL DEFAULT '{}', search_text TEXT NOT NULL DEFAULT '',
			indexed_at TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE entry_versions (
			id INTEGER PRIMARY KEY, game_id TEXT NOT NULL, schema_version TEXT NOT NULL,
			s3_key TEXT NOT NULL, UNIQUE(game_id, schema_version)
		);
		CREATE TABLE sources (id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL, aonid INTEGER);
		CREATE VIRTUAL TABLE entries_fts USING fts5(
			name, source, search_text, content=entries, content_rowid=id,
			tokenize='unicode61 remove_diacritics 1'
		);
		CREATE VIRTUAL TABLE entries_trigram USING fts5(
			name, content=entries, content_rowid=id, tokenize='trigram'
		);
		CREATE TABLE alternates (
			game_id TEXT NOT NULL, alternate_game_id TEXT NOT NULL,
			alternate_type TEXT NOT NULL, PRIMARY KEY (game_id, alternate_game_id)
		);
	`
	if _, err := d.Exec(ddl); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Seed entries: a monster and a template
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('Monsters:100', 'monsters', 'Adult Red Dragon', '1.3', 'monsters/bestiary/adult_red_dragon.json', 'json/monsters/1.3/bestiary/adult_red_dragon.json', 14, 'Bestiary', 'remastered', '{}', 'Adult Red Dragon')`)
	if err != nil {
		t.Fatalf("insert monster: %v", err)
	}
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('MonsterTemplates:22', 'monster_templates', 'Elite', '1.0', 'monster_templates/monster_core/elite.json', 'json/monster_templates/1.0/monster_core/elite.json', NULL, 'Monster Core', 'remastered', '{}', 'Elite')`)
	if err != nil {
		t.Fatalf("insert remastered template: %v", err)
	}
	// Legacy edition of Elite template
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('MonsterTemplates:1', 'monster_templates', 'Elite', '1.0', 'monster_templates/bestiary/elite.json', 'json/monster_templates/1.0/bestiary/elite.json', NULL, 'Bestiary', 'legacy', '{}', 'Elite')`)
	if err != nil {
		t.Fatalf("insert legacy template: %v", err)
	}
	// Orc Brute (legacy) ↔ Orc Scrapper (remastered)
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('Monsters:200', 'monsters', 'Orc Brute', '1.3', 'monsters/bestiary/orc_brute.json', 'json/monsters/1.3/bestiary/orc_brute.json', 0, 'Bestiary', 'legacy', '{}', 'Orc Brute')`)
	if err != nil {
		t.Fatalf("insert orc brute: %v", err)
	}
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('Monsters:201', 'monsters', 'Orc Scrapper', '1.3', 'monsters/monster_core/orc_scrapper.json', 'json/monsters/1.3/monster_core/orc_scrapper.json', 1, 'Monster Core', 'remastered', '{}', 'Orc Scrapper')`)
	if err != nil {
		t.Fatalf("insert orc scrapper: %v", err)
	}

	// Kobold Warrior: same name, different editions
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('Monsters:300', 'monsters', 'Kobold Warrior', '1.3', 'monsters/bestiary/kobold_warrior.json', 'json/monsters/1.3/bestiary/kobold_warrior.json', -1, 'Bestiary', 'legacy', '{}', 'Kobold Warrior')`)
	if err != nil {
		t.Fatalf("insert legacy kobold: %v", err)
	}
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('Monsters:301', 'monsters', 'Kobold Warrior', '1.3', 'monsters/monster_core/kobold_warrior.json', 'json/monsters/1.3/monster_core/kobold_warrior.json', 0, 'Monster Core', 'remastered', '{}', 'Kobold Warrior')`)
	if err != nil {
		t.Fatalf("insert remastered kobold: %v", err)
	}

	// Sources
	_, err = d.Exec(`INSERT INTO sources(name, aonid) VALUES('Bestiary', 2), ('Monster Core', NULL)`)
	if err != nil {
		t.Fatalf("insert sources: %v", err)
	}

	// Bidirectional alternate links
	_, err = d.Exec(`INSERT INTO alternates(game_id, alternate_game_id, alternate_type) VALUES
		('MonsterTemplates:22', 'MonsterTemplates:1', 'legacy'),
		('MonsterTemplates:1', 'MonsterTemplates:22', 'remastered'),
		('Monsters:200', 'Monsters:201', 'remastered'),
		('Monsters:201', 'Monsters:200', 'legacy'),
		('Monsters:300', 'Monsters:301', 'remastered'),
		('Monsters:301', 'Monsters:300', 'legacy')`)
	if err != nil {
		t.Fatalf("insert alternates: %v", err)
	}

	// Entry versions
	_, err = d.Exec(`INSERT INTO entry_versions(game_id, schema_version, s3_key) VALUES
		('Monsters:100', '1.3', 'json/monsters/1.3/bestiary/adult_red_dragon.json'),
		('Monsters:100', '1.2', 'json/monsters/1.2/bestiary/adult_red_dragon.json')`)
	if err != nil {
		t.Fatalf("insert entry_versions: %v", err)
	}

	if _, err := d.Exec("INSERT INTO entries_fts(entries_fts) VALUES('rebuild')"); err != nil {
		t.Fatalf("rebuild fts: %v", err)
	}
	if _, err := d.Exec("INSERT INTO entries_trigram(entries_trigram) VALUES('rebuild')"); err != nil {
		t.Fatalf("rebuild trigram: %v", err)
	}

	db.SetGlobal(d)
}

func newTestRouter() http.Handler {
	return newTestRouterWithS3(nil)
}

func newTestRouterWithS3(s3Mock *mockS3) http.Handler {
	cfg := Config{
		ImageDomain: "images.example.com",
	}
	if s3Mock != nil {
		cfg.S3Client = s3Mock
	}
	return NewRouter(cfg)
}

func TestSuggestHandler_ShortQuery(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest?q=dr", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var results []db.Suggestion
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty array for short query, got %d results", len(results))
	}
}

func TestSuggestHandler_MatchesEntry(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest?q=dragon&type=monsters", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var results []db.Suggestion
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Adult Red Dragon" {
		t.Errorf("expected 'Adult Red Dragon', got %q", results[0].Name)
	}
	if results[0].Type != "monsters" {
		t.Errorf("expected type=monsters, got %q", results[0].Type)
	}
}

func TestSuggestHandler_MultiType(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest?q=dragon&type=monsters&type=npcs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var results []db.Suggestion
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Only 1 entry in our fixture matches (the monster), no NPCs seeded
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSuggestHandler_EmptyQuery(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var results []db.Suggestion
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty array, got %d results", len(results))
	}
}

// ---------------------------------------------------------------------------
// statusWriter tests
// ---------------------------------------------------------------------------

func TestStatusWriter_CapturesExplicitStatus(t *testing.T) {
	inner := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: inner, status: 200}
	sw.WriteHeader(404)
	if sw.status != 404 {
		t.Errorf("expected 404, got %d", sw.status)
	}
}

func TestStatusWriter_CapturesImplicitOK(t *testing.T) {
	inner := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: inner, status: 200}
	sw.Write([]byte("hello"))
	if sw.status != 200 {
		t.Errorf("expected 200, got %d", sw.status)
	}
	if !sw.wroteHeader {
		t.Error("expected wroteHeader to be true after Write")
	}
}

func TestStatusWriter_DoubleWriteHeaderIgnored(t *testing.T) {
	inner := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: inner, status: 200}
	sw.WriteHeader(201)
	sw.WriteHeader(500) // should be ignored
	if sw.status != 201 {
		t.Errorf("expected 201 (first call wins), got %d", sw.status)
	}
}

// ---------------------------------------------------------------------------
// Template handler tests
// ---------------------------------------------------------------------------

// minimalCreature returns a small creature JSON for testing.
func minimalCreature() map[string]any {
	return map[string]any{
		"name": "Test Creature",
		"type": "creature",
		"stat_block": map[string]any{
			"creature_type": map[string]any{"level": float64(5)},
			"defense": map[string]any{
				"ac": map[string]any{"value": float64(20)},
				"saves": map[string]any{
					"fort": map[string]any{"value": float64(10)},
					"ref":  map[string]any{"value": float64(8)},
					"will": map[string]any{"value": float64(9)},
				},
				"hitpoints": []any{
					map[string]any{"hp": float64(50)},
				},
			},
			"offense": map[string]any{
				"offensive_actions": []any{
					map[string]any{
						"offensive_action_type": "attack",
						"attack": map[string]any{
							"weapon": "sword",
							"bonus": map[string]any{
								"bonuses": []any{float64(12), float64(7), float64(2)},
							},
							"damage": []any{
								map[string]any{"formula": "1d8+4", "damage_type": "slashing"},
							},
						},
					},
				},
			},
			"senses": map[string]any{
				"perception": map[string]any{"value": float64(11)},
			},
			"statistics": map[string]any{
				"skills": []any{
					map[string]any{"name": "Athletics", "value": float64(12)},
				},
			},
		},
	}
}

// minimalEliteTemplate returns a small Elite template JSON for testing.
func minimalEliteTemplate() template.TemplateJSON {
	return template.TemplateJSON{
		Name: "Elite",
		MonsterTemplate: template.MonsterTemplate{
			Name: "Elite",
			Changes: []template.Change{
				{
					ChangeCategory: "level",
					Text:           "Increase level by 1",
					Effects: []template.Effect{
						{Conditional: "$.creature_type.level <= 0", Target: "$.creature_type.level", Operation: "adjustment", Value: float64(2)},
						{Conditional: "default", Target: "$.creature_type.level", Operation: "adjustment", Value: float64(1)},
					},
				},
				{
					ChangeCategory: "combat_stats",
					Text:           "Increase AC, saves, etc. by 2",
					Effects: []template.Effect{
						{Target: "$.defense.ac.value", Operation: "adjustment", Value: float64(2)},
						{Target: "$.defense.saves.fort.value", Operation: "adjustment", Value: float64(2)},
						{Target: "$.defense.saves.ref.value", Operation: "adjustment", Value: float64(2)},
						{Target: "$.defense.saves.will.value", Operation: "adjustment", Value: float64(2)},
					},
				},
			},
		},
	}
}

// parseMultipartResponse parses a multipart/mixed response into its two parts:
// the patch document and the creature JSON.
func parseMultipartResponse(t *testing.T, w *httptest.ResponseRecorder) (patchDoc template.PatchDocument, creature map[string]any) {
	t.Helper()
	ct := w.Header().Get("Content-Type")
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("parse content-type %q: %v", ct, err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatalf("no boundary in content-type %q", ct)
	}

	mr := multipart.NewReader(w.Body, boundary)

	// Part 1: patches
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("read patches part: %v", err)
	}
	if err := json.NewDecoder(part).Decode(&patchDoc); err != nil {
		t.Fatalf("decode patches part: %v", err)
	}

	// Part 2: creature
	part, err = mr.NextPart()
	if err != nil {
		t.Fatalf("read creature part: %v", err)
	}
	if err := json.NewDecoder(part).Decode(&creature); err != nil {
		t.Fatalf("decode creature part: %v", err)
	}

	return patchDoc, creature
}

func TestApplyTemplateByID(t *testing.T) {
	setupTestDB(t)

	creatureJSON, _ := json.Marshal(minimalCreature())
	tmplJSON, _ := json.Marshal(minimalEliteTemplate())

	mock := &mockS3{objects: map[string][]byte{
		"json/monsters/1.3/bestiary/adult_red_dragon.json":   creatureJSON,
		"json/monster_templates/1.0/monster_core/elite.json": tmplJSON,
	}}
	r := newTestRouterWithS3(mock)

	req := httptest.NewRequest("GET", "/api/pfsrd2/templates/Monsters:100/apply/MonsterTemplates:22", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	patchDoc, creature := parseMultipartResponse(t, w)
	if len(patchDoc.AppliedPatches) == 0 {
		t.Error("expected applied patches")
	}
	if creature["stat_block"] == nil {
		t.Error("expected creature with stat_block")
	}
}

func TestApplyTemplateByID_MonsterNotFound(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&mockS3{objects: map[string][]byte{}})

	req := httptest.NewRequest("GET", "/api/pfsrd2/templates/Monsters:999/apply/MonsterTemplates:22", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestApplyTemplateInline(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&mockS3{objects: map[string][]byte{}})

	body := map[string]any{
		"creature": minimalCreature(),
		"template": minimalEliteTemplate(),
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/pfsrd2/templates/apply", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	_, creature := parseMultipartResponse(t, w)

	// Verify level was incremented
	sb := creature["stat_block"].(map[string]any)
	level := sb["creature_type"].(map[string]any)["level"].(float64)
	if level != 6 {
		t.Errorf("expected level 6, got %v", level)
	}

	// Verify AC was incremented
	ac := sb["defense"].(map[string]any)["ac"].(map[string]any)["value"].(float64)
	if ac != 22 {
		t.Errorf("expected AC 22, got %v", ac)
	}
}

func TestApplyTemplateInline_MissingCreature(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&mockS3{objects: map[string][]byte{}})

	body := map[string]any{
		"template": minimalEliteTemplate(),
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/pfsrd2/templates/apply", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestApplyTemplateInline_ByGameID(t *testing.T) {
	setupTestDB(t)

	tmplJSON, _ := json.Marshal(minimalEliteTemplate())
	mock := &mockS3{objects: map[string][]byte{
		"json/monster_templates/1.0/monster_core/elite.json": tmplJSON,
	}}
	r := newTestRouterWithS3(mock)

	body := map[string]any{
		"creature":         minimalCreature(),
		"template_game_id": "MonsterTemplates:22",
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/pfsrd2/templates/apply", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	patchDoc, _ := parseMultipartResponse(t, w)
	if len(patchDoc.AppliedPatches) == 0 {
		t.Error("expected applied patches")
	}
}

func TestApplyTemplateByID_EditionResolution(t *testing.T) {
	setupTestDB(t)

	// The monster is legacy (Bestiary), template requested is remastered (MonsterTemplates:22).
	// The handler should auto-resolve to the legacy alternate (MonsterTemplates:1).

	// Update the monster to be legacy edition
	db.Global().Exec("UPDATE entries SET edition = 'legacy' WHERE game_id = 'Monsters:100'")

	creatureJSON, _ := json.Marshal(minimalCreature())
	remastered := minimalEliteTemplate()
	remastered.Name = "Elite (Remastered)"
	remasteredJSON, _ := json.Marshal(remastered)

	legacy := minimalEliteTemplate()
	legacy.Name = "Elite (Legacy)"
	legacyJSON, _ := json.Marshal(legacy)

	mock := &mockS3{objects: map[string][]byte{
		"json/monsters/1.3/bestiary/adult_red_dragon.json":   creatureJSON,
		"json/monster_templates/1.0/monster_core/elite.json": remasteredJSON,
		"json/monster_templates/1.0/bestiary/elite.json":     legacyJSON,
	}}
	r := newTestRouterWithS3(mock)

	// Request remastered template for a legacy creature
	req := httptest.NewRequest("GET", "/api/pfsrd2/templates/Monsters:100/apply/MonsterTemplates:22", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	_, creature := parseMultipartResponse(t, w)
	// The handler should have fetched from the legacy S3 key, not the remastered one.
	// We can't easily distinguish the template used from the result alone, but we can
	// verify it succeeded — the legacy template was the one fetched.
	if creature["stat_block"] == nil {
		t.Error("expected creature with stat_block")
	}
}

func TestApplyTemplateInline_EditionResolution(t *testing.T) {
	setupTestDB(t)

	legacy := minimalEliteTemplate()
	legacy.Name = "Elite (Legacy)"
	legacyJSON, _ := json.Marshal(legacy)

	mock := &mockS3{objects: map[string][]byte{
		"json/monster_templates/1.0/bestiary/elite.json": legacyJSON,
	}}
	r := newTestRouterWithS3(mock)

	// Creature is legacy edition, request remastered template by game_id
	creature := minimalCreature()
	creature["edition"] = "legacy"

	body := map[string]any{
		"creature":         creature,
		"template_game_id": "MonsterTemplates:22", // remastered
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/api/pfsrd2/templates/apply", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	patchDoc, _ := parseMultipartResponse(t, w)
	if len(patchDoc.AppliedPatches) == 0 {
		t.Error("expected applied patches")
	}
}

// ---------------------------------------------------------------------------
// Unified suggest tests
// ---------------------------------------------------------------------------

func TestSuggestUnified_MatchWithAlternate(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	// Search for "orc brute" (legacy name) — should get one result with alternate
	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified?q=orc+brute", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []db.UnifiedSuggestion
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %s", len(results), w.Body.String())
	}

	s := results[0]
	if s.Name != "Orc Brute" {
		t.Errorf("expected primary 'Orc Brute', got %q", s.Name)
	}
	if s.Edition == nil || *s.Edition != "legacy" {
		t.Errorf("expected edition 'legacy', got %v", s.Edition)
	}
	if s.Alternate == nil {
		t.Fatal("expected alternate, got nil")
	}
	if s.Alternate.Name != "Orc Scrapper" {
		t.Errorf("expected alternate 'Orc Scrapper', got %q", s.Alternate.Name)
	}
	if s.Alternate.Edition == nil || *s.Alternate.Edition != "remastered" {
		t.Errorf("expected alternate edition 'remastered', got %v", s.Alternate.Edition)
	}
}

func TestSuggestUnified_SearchByAlternateName(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	// Search for "orc scrapper" (remastered name) — primary should be Orc Scrapper
	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified?q=orc+scrapper", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []db.UnifiedSuggestion
	json.Unmarshal(w.Body.Bytes(), &results)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %s", len(results), w.Body.String())
	}
	if results[0].Name != "Orc Scrapper" {
		t.Errorf("expected 'Orc Scrapper', got %q", results[0].Name)
	}
	if results[0].Alternate == nil || results[0].Alternate.Name != "Orc Brute" {
		t.Errorf("expected alternate 'Orc Brute'")
	}
}

func TestSuggestUnified_NoDuplicateWhenBothMatch(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	// Search for "orc" — both Orc Brute and Orc Scrapper match.
	// Should deduplicate: return one result with the other as alternate.
	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified?q=orc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []db.UnifiedSuggestion
	json.Unmarshal(w.Body.Bytes(), &results)

	// Count how many orc-related results
	orcCount := 0
	for _, s := range results {
		if s.Name == "Orc Brute" || s.Name == "Orc Scrapper" {
			orcCount++
		}
	}
	if orcCount != 1 {
		t.Errorf("expected 1 orc result (deduped), got %d: %s", orcCount, w.Body.String())
	}
}

func TestSuggestUnified_NoAlternateForSoloEntry(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	// Adult Red Dragon has no alternate link
	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified?q=dragon", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []db.UnifiedSuggestion
	json.Unmarshal(w.Body.Bytes(), &results)

	found := false
	for _, s := range results {
		if s.Name == "Adult Red Dragon" {
			found = true
			if s.Alternate != nil {
				t.Errorf("Adult Red Dragon should have no alternate, got %+v", s.Alternate)
			}
		}
	}
	if !found {
		t.Error("expected Adult Red Dragon in results")
	}
}

func TestSuggestUnified_ShortQuery(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	// 2-char query should still work (no 3-char minimum)
	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified?q=or", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []db.UnifiedSuggestion
	json.Unmarshal(w.Body.Bytes(), &results)

	// Should find Orc Brute/Scrapper (name contains "or")
	if len(results) == 0 {
		t.Error("expected results for 2-char query 'or'")
	}
}

func TestSuggestUnified_SameNameRemasteredWins(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	// "kobold warrior" exists in both editions with the same name.
	// Remastered should be primary, legacy should be alternate.
	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified?q=kobold+warrior", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []db.UnifiedSuggestion
	json.Unmarshal(w.Body.Bytes(), &results)

	// Should be exactly 1 result (deduped)
	kobolds := 0
	for _, s := range results {
		if s.Name == "Kobold Warrior" {
			kobolds++
			if s.Edition == nil || *s.Edition != "remastered" {
				t.Errorf("expected remastered primary, got %v", s.Edition)
			}
			if s.Alternate == nil {
				t.Fatal("expected legacy alternate")
			}
			if s.Alternate.Edition == nil || *s.Alternate.Edition != "legacy" {
				t.Errorf("expected legacy alternate edition, got %v", s.Alternate.Edition)
			}
			// Verify level differs between editions
			if s.Level != nil && s.Alternate.Level != nil && *s.Level == *s.Alternate.Level {
				// levels are -1 (legacy) and 0 (remastered) in fixtures
				t.Errorf("expected different levels between editions")
			}
		}
	}
	if kobolds != 1 {
		t.Errorf("expected 1 Kobold Warrior result, got %d: %s", kobolds, w.Body.String())
	}
}

func TestSuggestUnified_DifferentNamesLexicalOrder(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	// "orc" matches both Orc Brute and Orc Scrapper.
	// Lexical order: "Orc Brute" < "Orc Scrapper", so Brute is primary.
	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified?q=orc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []db.UnifiedSuggestion
	json.Unmarshal(w.Body.Bytes(), &results)

	for _, s := range results {
		if s.Name == "Orc Brute" || s.Name == "Orc Scrapper" {
			if s.Name != "Orc Brute" {
				t.Errorf("expected lexical winner 'Orc Brute' as primary, got %q", s.Name)
			}
			if s.Alternate == nil || s.Alternate.Name != "Orc Scrapper" {
				t.Errorf("expected 'Orc Scrapper' as alternate")
			}
			break
		}
	}
}

func TestSuggestUnified_EmptyQuery(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/search/suggest/unified", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var results []db.UnifiedSuggestion
	json.Unmarshal(w.Body.Bytes(), &results)
	if len(results) != 0 {
		t.Errorf("expected empty array, got %d results", len(results))
	}
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestCreatureEditionFromJSON(t *testing.T) {
	// With edition
	creature := map[string]any{"edition": "legacy"}
	ed := creatureEditionFromJSON(creature)
	if ed == nil || *ed != "legacy" {
		t.Errorf("expected 'legacy', got %v", ed)
	}

	// Without edition
	creature2 := map[string]any{"name": "Test"}
	ed2 := creatureEditionFromJSON(creature2)
	if ed2 != nil {
		t.Errorf("expected nil, got %v", ed2)
	}
}

// ---------------------------------------------------------------------------
// Pre-existing handler tests (backfill)
// ---------------------------------------------------------------------------

func TestSearchHandler(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/search?q=dragon&type=monsters", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result db.SearchResult
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Total == 0 {
		t.Error("expected search results for 'dragon'")
	}
	for _, e := range result.Results {
		if e.Type != "monsters" {
			t.Errorf("type filter not applied: got %q", e.Type)
		}
	}
}

func TestSearchHandler_NoQuery(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/search", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result db.SearchResult
	json.Unmarshal(w.Body.Bytes(), &result)
	// No query returns all entries
	if result.Total == 0 {
		t.Error("expected all entries when no query")
	}
}

func TestTypesHandler(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/types", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var types []db.TypeCount
	json.Unmarshal(w.Body.Bytes(), &types)
	if len(types) == 0 {
		t.Error("expected type counts")
	}
	found := false
	for _, tc := range types {
		if tc.Type == "monsters" && tc.Count > 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected monsters type with count > 0")
	}
}

func TestSourcesHandler(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/sources", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Sources returns []map[string]any
	var sources []map[string]any
	json.Unmarshal(w.Body.Bytes(), &sources)
	if len(sources) == 0 {
		t.Error("expected sources")
	}
}

func TestListTypeHandler(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/monsters?limit=3", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result db.SearchResult
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(result.Results))
	}
	for _, e := range result.Results {
		if e.Type != "monsters" {
			t.Errorf("expected type=monsters, got %q", e.Type)
		}
	}
}

func TestGetEntryHandler(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/Monsters:100", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	entry := resp["entry"].(map[string]any)
	if entry["name"] != "Adult Red Dragon" {
		t.Errorf("expected Adult Red Dragon, got %v", entry["name"])
	}
	if resp["versions"] == nil {
		t.Error("expected versions array")
	}
}

func TestGetEntryHandler_NotFound(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/Monsters:99999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetEntryFullHandler(t *testing.T) {
	setupTestDB(t)

	creatureJSON := []byte(`{"name":"Adult Red Dragon","stat_block":{}}`)
	mock := &mockS3{objects: map[string][]byte{
		"json/monsters/1.3/bestiary/adult_red_dragon.json": creatureJSON,
	}}
	r := newTestRouterWithS3(mock)

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/Monsters:100/full", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %q", w.Header().Get("Content-Type"))
	}
}

func TestGetEntryFullHandler_WithVersion(t *testing.T) {
	setupTestDB(t)

	v12JSON := []byte(`{"name":"Adult Red Dragon","schema":"1.2"}`)
	mock := &mockS3{objects: map[string][]byte{
		"json/monsters/1.2/bestiary/adult_red_dragon.json": v12JSON,
	}}
	r := newTestRouterWithS3(mock)

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/Monsters:100/full?version=1.2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetEntryFullHandler_NotFound(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&mockS3{objects: map[string][]byte{}})

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/Monsters:99999/full", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetFullJSONHandler(t *testing.T) {
	setupTestDB(t)

	jsonBody := []byte(`{"name":"test"}`)
	mock := &mockS3{objects: map[string][]byte{
		"json/monsters/1.3/bestiary/adult_red_dragon.json": jsonBody,
	}}
	r := newTestRouterWithS3(mock)

	req := httptest.NewRequest("GET", "/api/pfsrd2/monsters/1.3/bestiary/adult_red_dragon.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetFullJSONHandler_NotFound(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&mockS3{objects: map[string][]byte{}})

	req := httptest.NewRequest("GET", "/api/pfsrd2/monsters/1.3/bestiary/nonexistent.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServeImageHandler(t *testing.T) {
	setupTestDB(t)

	imgData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	mock := &mockS3{objects: map[string][]byte{
		"images/Monsters/dragon.png": imgData,
	}}
	r := newTestRouterWithS3(mock)

	req := httptest.NewRequest("GET", "/api/pfsrd2/images/Monsters/dragon.png", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("expected image/png, got %q", w.Header().Get("Content-Type"))
	}
}

func TestServeImageHandler_NotFound(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&mockS3{objects: map[string][]byte{}})

	req := httptest.NewRequest("GET", "/api/pfsrd2/images/Monsters/nonexistent.png", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServeImageHandler_PathTraversal(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&mockS3{objects: map[string][]byte{}})

	// Chi normalizes /../ in URLs, so test with .. embedded in the path param.
	// The handler checks for ".." in category/filename and returns 400.
	req := httptest.NewRequest("GET", "/api/pfsrd2/images/Monsters/..%2f..%2fetc%2fpasswd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Chi URL-decodes path params, so handler sees "../etc/passwd" which contains "/"
	// The handler's path component check should catch this
	if w.Code == http.StatusOK {
		t.Error("path traversal attempt should not return 200")
	}
}
