// Package knowledge provides a local FTS5-based knowledge store for project context.
// It indexes markdown docs, Go source files, session notes, and task summaries,
// allowing agents to query project knowledge through the query_knowledge MCP tool.
//
// The knowledge database is kept separate from the main state.sqlite because the
// state repository uses a full-replace save pattern (DELETE + INSERT all rows),
// which would destroy the FTS5 index on every write. The knowledge store instead
// uses incremental updates based on file checksums and modification times.
package knowledge

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Document represents a piece of indexed content.
type Document struct {
	Path     string // file path or state reference (e.g. "session_note:42")
	Title    string // filename or note title
	Content  string // full text content
	Category string // "markdown", "go_source", "session_note", "task_summary", "config"
}

// Result represents a search result from the knowledge store.
type Result struct {
	Path     string  `json:"path"`
	Title    string  `json:"title"`
	Snippet  string  `json:"snippet"`
	Category string  `json:"category"`
	Rank     float64 `json:"rank"`
}

const knowledgeSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS documents USING fts5(
	path,
	title,
	content,
	category,
	tokenize='porter unicode61'
);

CREATE TABLE IF NOT EXISTS doc_meta (
	path TEXT PRIMARY KEY,
	mod_time TEXT,
	checksum TEXT,
	indexed_at TEXT
);
`

// KnowledgeStore wraps a separate SQLite database with FTS5 tables
// for indexing and querying project knowledge.
type KnowledgeStore struct {
	db   *sql.DB
	mu   sync.RWMutex
	path string
}

// NewKnowledgeStore opens (or creates) a knowledge database at the given path.
func NewKnowledgeStore(dbPath string) (*KnowledgeStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create knowledge db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open knowledge db: %w", err)
	}

	if _, err := db.Exec(knowledgeSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init knowledge schema: %w", err)
	}

	return &KnowledgeStore{db: db, path: dbPath}, nil
}

// Index inserts or updates a document in the FTS5 index.
// If the document already exists (same path), it is replaced.
func (s *KnowledgeStore) Index(doc Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Remove old entry from FTS if it exists
	if _, err := tx.Exec(`DELETE FROM documents WHERE path = ?`, doc.Path); err != nil {
		return fmt.Errorf("delete old doc: %w", err)
	}

	// Insert into FTS5
	if _, err := tx.Exec(
		`INSERT INTO documents (path, title, content, category) VALUES (?, ?, ?, ?)`,
		doc.Path, doc.Title, doc.Content, doc.Category,
	); err != nil {
		return fmt.Errorf("insert doc: %w", err)
	}

	// Update metadata
	checksum := checksumString(doc.Content)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO doc_meta (path, mod_time, checksum, indexed_at) VALUES (?, ?, ?, ?)`,
		doc.Path, now, checksum, now,
	); err != nil {
		return fmt.Errorf("upsert doc_meta: %w", err)
	}

	return tx.Commit()
}

// IndexIfChanged indexes a document only if its content has changed (by checksum).
// Returns true if the document was (re)indexed, false if unchanged.
func (s *KnowledgeStore) IndexIfChanged(doc Document) (bool, error) {
	newChecksum := checksumString(doc.Content)

	s.mu.RLock()
	var existingChecksum string
	err := s.db.QueryRow(`SELECT checksum FROM doc_meta WHERE path = ?`, doc.Path).Scan(&existingChecksum)
	s.mu.RUnlock()

	if err == nil && existingChecksum == newChecksum {
		return false, nil
	}

	if err := s.Index(doc); err != nil {
		return false, err
	}
	return true, nil
}

// Remove deletes a document from the FTS5 index and metadata.
func (s *KnowledgeStore) Remove(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM documents WHERE path = ?`, path); err != nil {
		return fmt.Errorf("delete from fts: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM doc_meta WHERE path = ?`, path); err != nil {
		return fmt.Errorf("delete from meta: %w", err)
	}

	return tx.Commit()
}

// RemoveByPrefix removes all documents whose path starts with the given prefix.
func (s *KnowledgeStore) RemoveByPrefix(prefix string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM documents WHERE path >= ? AND path < ?`, prefix, prefix+"\xff")
	if err != nil {
		return 0, fmt.Errorf("delete from fts: %w", err)
	}
	count, _ := res.RowsAffected()

	if _, err := tx.Exec(`DELETE FROM doc_meta WHERE path >= ? AND path < ?`, prefix, prefix+"\xff"); err != nil {
		return 0, fmt.Errorf("delete from meta: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}

// Query searches the knowledge index using FTS5 MATCH syntax.
// Returns up to limit results sorted by relevance rank.
func (s *KnowledgeStore) Query(query string, category string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 10
	}
	if query == "" {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build FTS5 query: search in content and title columns
	ftsQuery := sanitizeFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	var rows *sql.Rows
	var err error

	if category != "" {
		rows, err = s.db.Query(`
			SELECT path, title, snippet(documents, 2, '>>>', '<<<', '...', 40) as snip, category, rank
			FROM documents
			WHERE documents MATCH ?
			AND category = ?
			ORDER BY rank
			LIMIT ?
		`, ftsQuery, category, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT path, title, snippet(documents, 2, '>>>', '<<<', '...', 40) as snip, category, rank
			FROM documents
			WHERE documents MATCH ?
			ORDER BY rank
			LIMIT ?
		`, ftsQuery, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("fts query: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Path, &r.Title, &r.Snippet, &r.Category, &r.Rank); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		results = append(results, r)
	}

	return results, rows.Err()
}

// IndexedPaths returns all paths currently in the index.
func (s *KnowledgeStore) IndexedPaths() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT path FROM doc_meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// Stats returns basic statistics about the knowledge index.
func (s *KnowledgeStore) Stats() (total int, byCategory map[string]int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byCategory = make(map[string]int)

	rows, err := s.db.Query(`SELECT category, COUNT(*) FROM doc_meta, documents ON doc_meta.path = documents.path GROUP BY category`)
	if err != nil {
		// Fallback: just count total
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM doc_meta`).Scan(&total)
		return total, byCategory, nil
	}
	defer rows.Close()

	for rows.Next() {
		var cat string
		var count int
		if err := rows.Scan(&cat, &count); err != nil {
			continue
		}
		byCategory[cat] = count
		total += count
	}
	return total, byCategory, nil
}

// Close closes the underlying database connection.
func (s *KnowledgeStore) Close() error {
	return s.db.Close()
}

// sanitizeFTSQuery converts a natural language query into a safe FTS5 query.
// It tokenizes the input and joins tokens with implicit AND logic.
func sanitizeFTSQuery(q string) string {
	// Remove FTS5 special characters that could cause syntax errors
	replacer := strings.NewReplacer(
		"\"", "",
		"'", "",
		"(", "",
		")", "",
		"*", "",
		":", "",
		"^", "",
		"{", "",
		"}", "",
	)
	cleaned := replacer.Replace(q)

	// Split into tokens and filter empties
	words := strings.Fields(cleaned)
	var tokens []string
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w != "" && w != "AND" && w != "OR" && w != "NOT" && w != "NEAR" {
			tokens = append(tokens, w)
		}
	}
	if len(tokens) == 0 {
		return ""
	}

	// Join with spaces (FTS5 implicit AND)
	return strings.Join(tokens, " ")
}

// checksumString computes a SHA-256 hex digest of a string.
func checksumString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ChecksumFile computes a SHA-256 hex digest of a file's contents.
func ChecksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
