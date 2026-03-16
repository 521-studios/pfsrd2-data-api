package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/521studios/pfsrd2-data-api/internal/db"
	_ "modernc.org/sqlite"
)

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
	`
	if _, err := d.Exec(ddl); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Seed a few entries
	_, err = d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, level, source, edition, attrs, search_text)
		VALUES('Monsters:100', 'monsters', 'Adult Red Dragon', '1.3', 'monsters/bestiary/adult_red_dragon.json', 'json/monsters/1.3/bestiary/adult_red_dragon.json', 14, 'Bestiary', 'remastered', '{}', 'Adult Red Dragon')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
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
	return NewRouter(Config{
		ImageDomain: "images.example.com",
	})
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
