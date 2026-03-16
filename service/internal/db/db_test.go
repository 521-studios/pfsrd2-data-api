package db

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// testDB creates an in-memory SQLite database with schema and fixture data.
// Returns the db and a cleanup function.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Schema — mirrors pfsrd2-automation/pfsrd2/indexer/schema.py
	ddl := `
		CREATE TABLE entries (
			id                     INTEGER PRIMARY KEY,
			game_id                TEXT UNIQUE NOT NULL,
			aonid                  INTEGER,
			type                   TEXT NOT NULL,
			name                   TEXT NOT NULL,
			current_schema_version TEXT NOT NULL,
			base_path              TEXT NOT NULL,
			s3_key                 TEXT NOT NULL,
			level                  INTEGER,
			source                 TEXT,
			source_page            INTEGER,
			edition                TEXT,
			image_s3_key           TEXT,
			attrs                  TEXT NOT NULL DEFAULT '{}',
			search_text            TEXT NOT NULL DEFAULT '',
			indexed_at             TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE entry_versions (
			id             INTEGER PRIMARY KEY,
			game_id        TEXT NOT NULL REFERENCES entries(game_id),
			schema_version TEXT NOT NULL,
			s3_key         TEXT NOT NULL,
			UNIQUE(game_id, schema_version)
		);
		CREATE TABLE sources (
			id    INTEGER PRIMARY KEY,
			name  TEXT UNIQUE NOT NULL,
			aonid INTEGER
		);
		CREATE VIRTUAL TABLE entries_fts USING fts5(
			name, source, search_text,
			content=entries, content_rowid=id,
			tokenize='unicode61 remove_diacritics 1'
		);
		CREATE VIRTUAL TABLE entries_trigram USING fts5(
			name, content=entries, content_rowid=id,
			tokenize='trigram'
		);
	`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("schema: %v", err)
	}

	seedFixtures(t, db)
	return db
}

func seedFixtures(t *testing.T, db *sql.DB) {
	t.Helper()

	entries := []struct {
		gameID, typ, name, schema, basePath, s3Key string
		level                                      int
		source, edition                            string
		imagePath                                  *string
	}{
		{"Monsters:100", "monsters", "Adult Red Dragon", "1.3", "monsters/bestiary/adult_red_dragon.json", "json/monsters/1.3/bestiary/adult_red_dragon.json", 14, "Bestiary", "remastered", nil},
		{"Monsters:101", "monsters", "Pseudodragon", "1.3", "monsters/bestiary/pseudodragon.json", "json/monsters/1.3/bestiary/pseudodragon.json", 2, "Bestiary", "remastered", strPtr("images/Monsters/Pseudodragon.webp")},
		{"Monsters:102", "monsters", "Young Blue Dragon", "1.3", "monsters/bestiary/young_blue_dragon.json", "json/monsters/1.3/bestiary/young_blue_dragon.json", 9, "Bestiary", "legacy", nil},
		{"NPCs:200", "npcs", "Dragon Cultist", "1.3", "npcs/gamemastery_guide/dragon_cultist.json", "json/npcs/1.3/gamemastery_guide/dragon_cultist.json", 3, "Gamemastery Guide", "remastered", nil},
		{"Spells:300", "spells", "Dragon Breath", "1.3", "spells/core_rulebook/dragon_breath.json", "json/spells/1.3/core_rulebook/dragon_breath.json", 4, "Core Rulebook", "remastered", nil},
		{"Monsters:400", "monsters", "Orc Brute", "1.3", "monsters/bestiary/orc_brute.json", "json/monsters/1.3/bestiary/orc_brute.json", 0, "Bestiary", "remastered", nil},
		{"Monsters:401", "monsters", "Orc Warchief", "1.3", "monsters/bestiary/orc_warchief.json", "json/monsters/1.3/bestiary/orc_warchief.json", 2, "Bestiary", "remastered", nil},
		{"Monsters:402", "monsters", "Giant Porcupine", "1.3", "monsters/bestiary/giant_porcupine.json", "json/monsters/1.3/bestiary/giant_porcupine.json", 2, "Bestiary", "remastered", nil},
		{"Monsters:403", "monsters", "Orc Warrior", "1.3", "monsters/bestiary/orc_warrior.json", "json/monsters/1.3/bestiary/orc_warrior.json", 1, "Bestiary", "remastered", nil},
	}

	for _, e := range entries {
		_, err := db.Exec(`
			INSERT INTO entries(game_id, aonid, type, name, current_schema_version,
				base_path, s3_key, level, source, edition, image_s3_key, attrs, search_text)
			VALUES(?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', ?)`,
			e.gameID, e.typ, e.name, e.schema, e.basePath, e.s3Key,
			e.level, e.source, e.edition, e.imagePath, e.name)
		if err != nil {
			t.Fatalf("insert entry %s: %v", e.gameID, err)
		}
	}

	// entry_versions: all entries have 1.3, some also have 1.2
	versions := []struct {
		gameID, schema, s3Key string
	}{
		{"Monsters:100", "1.3", "json/monsters/1.3/bestiary/adult_red_dragon.json"},
		{"Monsters:100", "1.2", "json/monsters/1.2/bestiary/adult_red_dragon.json"},
		{"Monsters:101", "1.3", "json/monsters/1.3/bestiary/pseudodragon.json"},
		{"Monsters:101", "1.2", "json/monsters/1.2/bestiary/pseudodragon.json"},
		{"Monsters:102", "1.3", "json/monsters/1.3/bestiary/young_blue_dragon.json"},
		// 102 intentionally has NO 1.2 version
		{"NPCs:200", "1.3", "json/npcs/1.3/gamemastery_guide/dragon_cultist.json"},
		{"NPCs:200", "1.2", "json/npcs/1.2/gamemastery_guide/dragon_cultist.json"},
		{"Spells:300", "1.3", "json/spells/1.3/core_rulebook/dragon_breath.json"},
	}
	for _, v := range versions {
		_, err := db.Exec(`INSERT INTO entry_versions(game_id, schema_version, s3_key) VALUES(?, ?, ?)`,
			v.gameID, v.schema, v.s3Key)
		if err != nil {
			t.Fatalf("insert version %s/%s: %v", v.gameID, v.schema, err)
		}
	}

	// Rebuild FTS indexes
	if _, err := db.Exec("INSERT INTO entries_fts(entries_fts) VALUES('rebuild')"); err != nil {
		t.Fatalf("rebuild fts: %v", err)
	}
	if _, err := db.Exec("INSERT INTO entries_trigram(entries_trigram) VALUES('rebuild')"); err != nil {
		t.Fatalf("rebuild trigram: %v", err)
	}
}

func strPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Suggest tests
// ---------------------------------------------------------------------------

func TestSuggest_ShortQuery(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "dr"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for short query, got %d", len(results))
	}
	if results == nil {
		t.Error("expected non-nil slice, got nil")
	}
}

func TestSuggest_SubstringMatch(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "dragon"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should match: Adult Red Dragon, Pseudodragon, Young Blue Dragon, Dragon Cultist, Dragon Breath
	if len(results) != 5 {
		t.Errorf("expected 5 results for 'dragon', got %d", len(results))
		for _, r := range results {
			t.Logf("  %s: %s", r.GameID, r.Name)
		}
	}
}

func TestSuggest_TypeFilter(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only monsters: Adult Red Dragon, Pseudodragon, Young Blue Dragon
	if len(results) != 3 {
		t.Errorf("expected 3 monster results, got %d", len(results))
	}
	for _, r := range results {
		if r.Type != "monsters" {
			t.Errorf("expected type=monsters, got %s for %s", r.Type, r.Name)
		}
	}
}

func TestSuggest_MultiTypeFilter(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Types: []string{"monsters", "npcs"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Monsters (3) + NPCs (1) = 4
	if len(results) != 4 {
		t.Errorf("expected 4 results for monsters+npcs, got %d", len(results))
	}
}

func TestSuggest_VersionFilter(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:       "dragon",
		Types:   []string{"monsters"},
		Version: "1.2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only monsters with 1.2: Adult Red Dragon, Pseudodragon (Young Blue has no 1.2)
	if len(results) != 2 {
		t.Errorf("expected 2 results with version=1.2, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s: %s", r.GameID, r.Name)
		}
	}
}

func TestSuggest_LimitCap(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit=2, got %d", len(results))
	}
}

func TestSuggest_LimitDefault(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Limit: 0, // should default to 15
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All 5 entries match, and default limit (15) is higher
	if len(results) != 5 {
		t.Errorf("expected 5 results with default limit, got %d", len(results))
	}
}

func TestSuggest_ImageIncluded(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "pseudodragon",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ImageS3Key == nil || *results[0].ImageS3Key != "images/Monsters/Pseudodragon.webp" {
		t.Errorf("expected image key, got %v", results[0].ImageS3Key)
	}
}

func TestSuggest_NoMatch(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "zzzznothing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if results == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestSuggest_WordBoundary(t *testing.T) {
	db := testDB(t)
	// "orc" matches substring: Orc Brute, Orc Warchief, Giant Porcupine, Orc Warrior
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "orc",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("expected 4 results for 'orc' substring, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s", r.Name)
		}
	}

	// "orc " (trailing space) should filter to word-boundary only — no Porcupine
	results, err = Suggest(context.Background(), db, SuggestParams{
		Q:     "orc ",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results for 'orc ' word-boundary, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s", r.Name)
		}
	}
	for _, r := range results {
		if r.Name == "Giant Porcupine" {
			t.Error("Porcupine should not match word-boundary 'orc '")
		}
	}
}

func TestSuggest_MultiWordPartial(t *testing.T) {
	db := testDB(t)
	// "orc w" — "orc" is complete word, "w" is partial substring
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "orc w",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should match: Orc Warchief, Orc Warrior (orc at word boundary + contains "w")
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'orc w', got %d", len(results))
		for _, r := range results {
			t.Logf("  %s", r.Name)
		}
	}
}

func TestSuggest_PrefixMatchFirst(t *testing.T) {
	db := testDB(t)
	// "dragon" should return prefix matches (Dragon Cultist, Dragon Breath) before
	// substring matches (Adult Red Dragon, Pseudodragon, Young Blue Dragon)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "dragon"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// First results should be prefix matches
	for _, r := range results[:2] {
		if r.Name != "Dragon Breath" && r.Name != "Dragon Cultist" {
			t.Errorf("expected prefix match in first 2 results, got %q", r.Name)
		}
	}
}
