// Package db manages the SQLite connection and provides query functions
// for the pfsrd2 search index.
//
// The DB is downloaded from S3 to /tmp at cold start and periodically refreshed
// by the watcher. All queries go through the singleton opened here.
//
// Extending queries:
//   - FTS search: add fields to SearchParams, extend the WHERE clause in Search()
//   - New type-specific filters: use json_extract(attrs, '$.field') in SQL
//   - New structured columns: add to entries table in schema.py and mirror here
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Singleton
// ---------------------------------------------------------------------------

var (
	mu       sync.RWMutex
	globalDB *sql.DB
)

// Open opens (or re-opens) the SQLite DB at path. Thread-safe — callers use
// ReplaceDB to atomically swap to a freshly downloaded copy.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_synchronous=NORMAL&mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer, but reads are fine
	return db, nil
}

// SetGlobal replaces the package-level DB. Existing queries complete before the
// old connection is closed.
func SetGlobal(db *sql.DB) {
	mu.Lock()
	old := globalDB
	globalDB = db
	mu.Unlock()
	if old != nil {
		old.Close()
	}
}

// Global returns the package-level DB (read lock held by caller via queries).
func Global() *sql.DB {
	mu.RLock()
	defer mu.RUnlock()
	return globalDB
}

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

// Entry is a single row from the entries table, with attrs decoded.
type Entry struct {
	ID                   int64           `json:"id"`
	GameID               string          `json:"game_id"`
	AonID                *int64          `json:"aonid,omitempty"`
	Type                 string          `json:"type"`
	Name                 string          `json:"name"`
	CurrentSchemaVersion string          `json:"current_schema_version"`
	BasePath             string          `json:"base_path"`
	S3Key                string          `json:"s3_key"`
	Level                *int            `json:"level,omitempty"`
	Source               *string         `json:"source,omitempty"`
	SourcePage           *int            `json:"source_page,omitempty"`
	Edition              *string         `json:"edition,omitempty"`
	ImageS3Key           *string         `json:"image_s3_key,omitempty"`
	Attrs                json.RawMessage `json:"attrs"`
	IndexedAt            string          `json:"indexed_at"`
}

// EntryVersion represents a (game_id, schema_version) pair from S3.
type EntryVersion struct {
	GameID        string `json:"game_id"`
	SchemaVersion string `json:"schema_version"`
	S3Key         string `json:"s3_key"`
}

// TypeCount is a type name with its entry count.
type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// SearchParams mirrors the GET /search query parameters.
type SearchParams struct {
	Q       string // full-text query (FTS5 prefix search)
	Type    string
	Level   string // "3" or "1-5" (range)
	Source  string
	Edition string
	Traits  string // comma-separated
	Limit   int    // default 20
	Offset  int
}

// SearchResult is returned by Search.
type SearchResult struct {
	Results []Entry `json:"results"`
	Total   int     `json:"total"`
}

// Search runs a hybrid FTS5 + structured-filter query over entries.
func Search(ctx context.Context, db *sql.DB, p SearchParams) (*SearchResult, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}

	args := []any{}
	conds := []string{}

	// FTS5 match — uses the entries_fts virtual table with BM25 ranking
	if p.Q != "" {
		conds = append(conds, "e.id IN (SELECT rowid FROM entries_fts WHERE entries_fts MATCH ?)")
		args = append(args, p.Q+"*")
	}

	// Structured filters
	if p.Type != "" {
		conds = append(conds, "e.type = ?")
		args = append(args, p.Type)
	}
	if p.Source != "" {
		conds = append(conds, "e.source = ?")
		args = append(args, p.Source)
	}
	if p.Edition != "" {
		conds = append(conds, "e.edition = ?")
		args = append(args, p.Edition)
	}
	if p.Traits != "" {
		// Filter: any of the requested traits appear in attrs.traits JSON array.
		// We use json_each for portability across SQLite versions.
		for _, trait := range strings.Split(p.Traits, ",") {
			t := strings.TrimSpace(trait)
			if t == "" {
				continue
			}
			conds = append(conds,
				"EXISTS (SELECT 1 FROM json_each(json_extract(e.attrs,'$.traits')) WHERE value = ?)")
			args = append(args, t)
		}
	}
	if p.Level != "" {
		if strings.Contains(p.Level, "-") {
			parts := strings.SplitN(p.Level, "-", 2)
			conds = append(conds, "e.level BETWEEN ? AND ?")
			args = append(args, parts[0], parts[1])
		} else {
			conds = append(conds, "e.level = ?")
			args = append(args, p.Level)
		}
	}

	where := "1=1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}

	// Count query
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM entries e WHERE %s", where)
	var total int
	if err := db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	// Data query — rank by FTS BM25 when Q is set, else by name.
	// BM25 requires joining the FTS table directly.
	ftsJoin := ""
	orderBy := "e.name ASC"
	if p.Q != "" {
		ftsJoin = "JOIN entries_fts ON entries_fts.rowid = e.id"
		orderBy = "bm25(entries_fts) ASC"
	}

	dataSQL := fmt.Sprintf(`
		SELECT e.id, e.game_id, e.aonid, e.type, e.name,
		       e.current_schema_version, e.base_path, e.s3_key,
		       e.level, e.source, e.source_page, e.edition,
		       e.image_s3_key, e.attrs, e.indexed_at
		FROM entries e
		%s
		WHERE %s
		ORDER BY %s
		LIMIT ? OFFSET ?
	`, ftsJoin, where, orderBy)
	dataArgs := append(args, p.Limit, p.Offset)

	rows, err := db.QueryContext(ctx, dataSQL, dataArgs...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	entries, err := scanEntries(rows)
	if err != nil {
		return nil, err
	}
	return &SearchResult{Results: entries, Total: total}, nil
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// ListParams for GET /{type}
type ListParams struct {
	Type    string
	Source  string
	Edition string
	Level   string
	Limit   int
	Offset  int
}

// List returns a paginated list of entries for a given type.
func List(ctx context.Context, db *sql.DB, p ListParams) (*SearchResult, error) {
	return Search(ctx, db, SearchParams{
		Type:    p.Type,
		Source:  p.Source,
		Edition: p.Edition,
		Level:   p.Level,
		Limit:   p.Limit,
		Offset:  p.Offset,
	})
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

// GetByGameID fetches a single entry by game_id.
func GetByGameID(ctx context.Context, db *sql.DB, gameID string) (*Entry, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, game_id, aonid, type, name,
		       current_schema_version, base_path, s3_key,
		       level, source, source_page, edition,
		       image_s3_key, attrs, indexed_at
		FROM entries WHERE game_id = ?
	`, gameID)
	var e Entry
	var attrsStr string
	err := row.Scan(
		&e.ID, &e.GameID, &e.AonID, &e.Type, &e.Name,
		&e.CurrentSchemaVersion, &e.BasePath, &e.S3Key,
		&e.Level, &e.Source, &e.SourcePage, &e.Edition,
		&e.ImageS3Key, &attrsStr, &e.IndexedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get entry: %w", err)
	}
	e.Attrs = json.RawMessage(attrsStr)
	return &e, nil
}

// GetVersions returns all known schema versions for a game_id.
func GetVersions(ctx context.Context, db *sql.DB, gameID string) ([]EntryVersion, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT game_id, schema_version, s3_key
		FROM entry_versions WHERE game_id = ?
		ORDER BY schema_version
	`, gameID)
	if err != nil {
		return nil, fmt.Errorf("query entry_versions: %w", err)
	}
	defer rows.Close()
	var versions []EntryVersion
	for rows.Next() {
		var v EntryVersion
		if err := rows.Scan(&v.GameID, &v.SchemaVersion, &v.S3Key); err != nil {
			return nil, fmt.Errorf("scan entry_version: %w", err)
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// GetAlternateGameID looks up the alternate edition of an entry. For example,
// given a remastered template's game_id and edition "legacy", it returns the
// legacy version's game_id (if one exists).
func GetAlternateGameID(ctx context.Context, db *sql.DB, gameID, wantEdition string) (string, error) {
	var altGameID string
	err := db.QueryRowContext(ctx, `
		SELECT alternate_game_id FROM alternates
		WHERE game_id = ? AND alternate_type = ?
	`, gameID, wantEdition).Scan(&altGameID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get alternate: %w", err)
	}
	return altGameID, nil
}

// ---------------------------------------------------------------------------
// Types / Sources
// ---------------------------------------------------------------------------

// Types returns all content types with their entry counts.
func Types(ctx context.Context, db *sql.DB) ([]TypeCount, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT type, COUNT(*) as count
		FROM entries GROUP BY type ORDER BY type
	`)
	if err != nil {
		return nil, fmt.Errorf("query types: %w", err)
	}
	defer rows.Close()
	var types []TypeCount
	for rows.Next() {
		var t TypeCount
		if err := rows.Scan(&t.Type, &t.Count); err != nil {
			return nil, fmt.Errorf("scan type count: %w", err)
		}
		types = append(types, t)
	}
	return types, rows.Err()
}

// Sources returns all source books from the sources table.
func Sources(ctx context.Context, db *sql.DB) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, aonid FROM sources ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query sources: %w", err)
	}
	defer rows.Close()
	var sources []map[string]any
	for rows.Next() {
		var name string
		var aonid *int
		if err := rows.Scan(&name, &aonid); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sources = append(sources, map[string]any{"name": name, "aonid": aonid})
	}
	return sources, rows.Err()
}

// ---------------------------------------------------------------------------
// Suggest (typeahead)
// ---------------------------------------------------------------------------

// Suggestion is a minimal entry for typeahead results.
type Suggestion struct {
	GameID     string  `json:"game_id"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Level      *int    `json:"level,omitempty"`
	ImageS3Key *string `json:"image_s3_key,omitempty"`
}

// SuggestParams for GET /search/suggest.
type SuggestParams struct {
	Q       string   // trigram search query (min 3 chars)
	Types   []string // content types to include
	Version string   // only entries with this schema version in entry_versions
	Limit   int      // hard cap at 15
}

// suggestShortQuery handles queries where all words are < 3 chars (too short for trigrams).
// Falls back to an exact name match so creatures like "I" can still be found.
func suggestShortQuery(ctx context.Context, db *sql.DB, p SuggestParams, query string) ([]Suggestion, error) {
	if p.Limit <= 0 || p.Limit > 15 {
		p.Limit = 15
	}

	args := []any{}
	conds := []string{"LOWER(e.name) = ?"}
	args = append(args, strings.ToLower(query))

	if len(p.Types) > 0 {
		placeholders := make([]string, len(p.Types))
		for i, t := range p.Types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		conds = append(conds, "e.type IN ("+strings.Join(placeholders, ",")+")")
	}

	if p.Version != "" {
		conds = append(conds,
			"EXISTS (SELECT 1 FROM entry_versions ev WHERE ev.game_id = e.game_id AND ev.schema_version = ?)")
		args = append(args, p.Version)
	}

	where := strings.Join(conds, " AND ")
	sqlQuery := fmt.Sprintf(`
		SELECT e.game_id, e.name, e.type, e.level, e.image_s3_key
		FROM entries e
		WHERE %s
		ORDER BY e.name ASC
		LIMIT ?
	`, where)
	args = append(args, p.Limit)

	rows, err := db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("suggest short query: %w", err)
	}
	defer rows.Close()

	results := make([]Suggestion, 0, p.Limit)
	for rows.Next() {
		var s Suggestion
		if err := rows.Scan(&s.GameID, &s.Name, &s.Type, &s.Level, &s.ImageS3Key); err != nil {
			return nil, fmt.Errorf("scan suggestion: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// buildWordMatchConds builds SQL conditions for word-based matching.
// Words with 3+ chars use trigram index; completed words (followed by space)
// use word-boundary LIKE; short partial words use LIKE substring.
func buildWordMatchConds(words []string, hasTrailingSpace bool) (conds []string, args []any) {
	for i, word := range words {
		isComplete := i < len(words)-1 || hasTrailingSpace

		if len(word) >= 3 {
			conds = append(conds,
				"e.id IN (SELECT rowid FROM entries_trigram WHERE name MATCH ?)")
			args = append(args, word)
		}

		if isComplete {
			lowerWord := strings.ToLower(word)
			conds = append(conds,
				"(LOWER(e.name) LIKE ? OR LOWER(e.name) LIKE ? OR LOWER(e.name) LIKE ? OR LOWER(e.name) = ?)")
			args = append(args, lowerWord+" %", "% "+lowerWord+" %", "% "+lowerWord, lowerWord)
		} else if len(word) < 3 {
			conds = append(conds, "LOWER(e.name) LIKE ?")
			args = append(args, "%"+strings.ToLower(word)+"%")
		}
	}
	return conds, args
}

// addTypeVersionFilters appends type and version filter conditions.
func addTypeVersionFilters(conds []string, args []any, types []string, version string) ([]string, []any) {
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		conds = append(conds, "e.type IN ("+strings.Join(placeholders, ",")+")")
	}
	if version != "" {
		conds = append(conds,
			"EXISTS (SELECT 1 FROM entry_versions ev WHERE ev.game_id = e.game_id AND ev.schema_version = ?)")
		args = append(args, version)
	}
	return conds, args
}

// Suggest runs a trigram substring search, returning minimal results for typeahead.
func Suggest(ctx context.Context, db *sql.DB, p SuggestParams) ([]Suggestion, error) {
	trimmed := strings.TrimRight(p.Q, " ")
	hasTrailingSpace := len(p.Q) > len(trimmed)
	words := strings.Fields(trimmed)

	if len(words) == 0 {
		return []Suggestion{}, nil
	}

	hasTrigram := false
	for _, w := range words {
		if len(w) >= 3 {
			hasTrigram = true
			break
		}
	}
	if !hasTrigram {
		return suggestShortQuery(ctx, db, p, trimmed)
	}
	if p.Limit <= 0 || p.Limit > 15 {
		p.Limit = 15
	}

	conds, args := buildWordMatchConds(words, hasTrailingSpace)
	conds, args = addTypeVersionFilters(conds, args, p.Types, p.Version)
	where := strings.Join(conds, " AND ")

	query := fmt.Sprintf(`
		SELECT e.game_id, e.name, e.type, e.level, e.image_s3_key
		FROM entries e
		WHERE %s
		ORDER BY CASE WHEN LOWER(e.name) LIKE ? THEN 0 ELSE 1 END, e.name ASC
		LIMIT ?
	`, where)
	args = append(args, strings.ToLower(trimmed)+"%", p.Limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("suggest: %w", err)
	}
	defer rows.Close()

	results := make([]Suggestion, 0, p.Limit)
	for rows.Next() {
		var s Suggestion
		if err := rows.Scan(&s.GameID, &s.Name, &s.Type, &s.Level, &s.ImageS3Key); err != nil {
			return nil, fmt.Errorf("scan suggestion: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// ---------------------------------------------------------------------------
// Unified Suggest (edition-aware typeahead)
// ---------------------------------------------------------------------------

// UnifiedSuggestion is a typeahead result that includes the entry's edition
// and optionally its cross-edition alternate.
type UnifiedSuggestion struct {
	GameID     string             `json:"game_id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Level      *int               `json:"level,omitempty"`
	Edition    *string            `json:"edition,omitempty"`
	ImageS3Key *string            `json:"image_s3_key,omitempty"`
	Alternate  *UnifiedSuggestion `json:"alternate,omitempty"`
}

// UnifiedSuggestParams for GET /search/suggest/unified.
type UnifiedSuggestParams struct {
	Q       string
	Types   []string
	Version string
	Limit   int
}

// SuggestUnified runs a typeahead search that joins alternates to produce
// edition-aware results. No minimum character requirement — 1-2 char queries
// use direct LIKE matching, 3+ use the trigram index.
func SuggestUnified(ctx context.Context, db *sql.DB, p UnifiedSuggestParams) ([]UnifiedSuggestion, error) {
	if p.Limit <= 0 || p.Limit > 15 {
		p.Limit = 15
	}

	trimmed := strings.TrimRight(p.Q, " ")
	hasTrailingSpace := len(p.Q) > len(trimmed)
	words := strings.Fields(trimmed)

	if len(words) == 0 {
		return []UnifiedSuggestion{}, nil
	}

	conds, args := buildUnifiedMatchConds(words, hasTrailingSpace, trimmed)
	conds, args = addTypeVersionFilters(conds, args, p.Types, p.Version)
	where := strings.Join(conds, " AND ")

	query := fmt.Sprintf(`
		SELECT e.game_id, e.name, e.type, e.level, e.edition, e.image_s3_key,
		       a2.game_id, a2.name, a2.type, a2.level, a2.edition, a2.image_s3_key
		FROM entries e
		LEFT JOIN alternates alt ON alt.game_id = e.game_id
		LEFT JOIN entries a2 ON a2.game_id = alt.alternate_game_id
		WHERE %s
		ORDER BY CASE WHEN LOWER(e.name) LIKE ? THEN 0 ELSE 1 END, e.name ASC
		LIMIT ?
	`, where)
	args = append(args, strings.ToLower(trimmed)+"%", p.Limit*2)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("suggest unified: %w", err)
	}
	defer rows.Close()

	rawRows, err := scanUnifiedRows(rows)
	if err != nil {
		return nil, err
	}

	return deduplicateUnified(rawRows, p.Limit), nil
}

// buildUnifiedMatchConds builds match conditions for unified suggest.
// Unlike Suggest, short queries (1-2 chars) use LIKE instead of being rejected.
func buildUnifiedMatchConds(words []string, hasTrailingSpace bool, trimmed string) ([]string, []any) {
	hasTrigram := false
	for _, w := range words {
		if len(w) >= 3 {
			hasTrigram = true
			break
		}
	}

	if !hasTrigram {
		return []string{"LOWER(e.name) LIKE ?"},
			[]any{"%" + strings.ToLower(trimmed) + "%"}
	}
	return buildWordMatchConds(words, hasTrailingSpace)
}

// unifiedRawRow pairs a primary match with its optional alternate.
type unifiedRawRow struct {
	primary   UnifiedSuggestion
	alternate *UnifiedSuggestion
}

// scanUnifiedRows reads all rows from the unified suggest query.
func scanUnifiedRows(rows *sql.Rows) ([]unifiedRawRow, error) {
	var rawRows []unifiedRawRow
	for rows.Next() {
		var s UnifiedSuggestion
		var altGameID, altName, altType, altEdition, altImage *string
		var altLevel *int

		if err := rows.Scan(
			&s.GameID, &s.Name, &s.Type, &s.Level, &s.Edition, &s.ImageS3Key,
			&altGameID, &altName, &altType, &altLevel, &altEdition, &altImage,
		); err != nil {
			return nil, fmt.Errorf("scan unified suggestion: %w", err)
		}

		r := unifiedRawRow{primary: s}
		if altGameID != nil {
			r.alternate = &UnifiedSuggestion{
				GameID:     *altGameID,
				Name:       deref(altName),
				Type:       deref(altType),
				Level:      altLevel,
				Edition:    altEdition,
				ImageS3Key: altImage,
			}
		}
		rawRows = append(rawRows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rawRows, nil
}

// deduplicateUnified merges alternate pairs into single results.
// When both editions matched: same name → remastered wins; different names → lexical order.
// When only one matched, it stays primary.
func deduplicateUnified(rawRows []unifiedRawRow, limit int) []UnifiedSuggestion {
	matched := map[string]bool{}
	for _, r := range rawRows {
		matched[r.primary.GameID] = true
	}

	seen := map[string]bool{}
	results := make([]UnifiedSuggestion, 0, limit)

	for _, r := range rawRows {
		if seen[r.primary.GameID] {
			continue
		}

		s := r.primary
		alt := r.alternate

		if alt != nil && seen[alt.GameID] {
			continue
		}

		if alt != nil {
			bothMatched := matched[s.GameID] && matched[alt.GameID]
			if bothMatched {
				if s.Name == alt.Name {
					if s.Edition != nil && *s.Edition == "legacy" {
						s, *alt = *alt, s
					}
				} else if s.Name > alt.Name {
					s, *alt = *alt, s
				}
			}
			s.Alternate = alt
			seen[alt.GameID] = true
		}

		seen[s.GameID] = true
		results = append(results, s)
		if len(results) >= limit {
			break
		}
	}

	return results
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---------------------------------------------------------------------------
// Row scanning helpers
// ---------------------------------------------------------------------------

type rowScanner interface {
	Scan(dest ...any) error
}

// wrappedRow adapts *sql.Row to the rowScanner interface for scanEntries.
type wrappedRow struct{ r *sql.Row }

func (w wrappedRow) Scan(dest ...any) error { return w.r.Scan(dest...) }
func (w wrappedRow) Next() bool             { return true }
func (w wrappedRow) Close() error           { return nil }
func (w wrappedRow) Err() error             { return nil }

// multiScanner is a minimal rows-like interface.
type multiScanner interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// singleRow wraps wrappedRow to implement multiScanner for a single *sql.Row.
type singleRow struct {
	w    wrappedRow
	done bool
}

func (s *singleRow) Next() bool {
	if s.done {
		return false
	}
	s.done = true
	return true
}
func (s *singleRow) Scan(d ...any) error { return s.w.Scan(d...) }
func (s *singleRow) Close() error        { return nil }
func (s *singleRow) Err() error          { return nil }

func scanEntries(rows multiScanner) ([]Entry, error) {
	var results []Entry
	for rows.Next() {
		var e Entry
		var attrsStr string
		if err := rows.Scan(
			&e.ID, &e.GameID, &e.AonID, &e.Type, &e.Name,
			&e.CurrentSchemaVersion, &e.BasePath, &e.S3Key,
			&e.Level, &e.Source, &e.SourcePage, &e.Edition,
			&e.ImageS3Key, &attrsStr, &e.IndexedAt,
		); err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		e.Attrs = json.RawMessage(attrsStr)
		results = append(results, e)
	}
	return results, rows.Err()
}
