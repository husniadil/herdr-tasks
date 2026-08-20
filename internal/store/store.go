// Package store is the SQLite side of the plugin: migrations, the entity
// tables, and their append-only event siblings (§5). It is the only code that
// opens the database file, and the daemon is the only process that runs it
// (§2.2). Every write goes through a transition helper so the mutation and its
// event land in one transaction (§5.5).
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/ids"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// Store owns the connection to one plugin database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the store at path.
func Open(path string) (*Store, error) {
	// §5.1: WAL, a 3 s busy timeout, foreign keys on. The pragmas ride on the
	// DSN so every connection in the pool gets them, not just the first.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, codes.Errorf(codes.Unavailable, "open %s: %v", path, err)
	}
	// The daemon is the only writer (§2.2) and SQLite writes serialize anyway;
	// one connection makes that explicit and keeps WAL contention out of the
	// picture entirely.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the connection.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for the few callers that need a raw read (doctor).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	var have int64
	err := s.db.QueryRow("SELECT schema_version FROM meta").Scan(&have)
	switch {
	case err != nil && !isMissingTable(err):
		return codes.Errorf(codes.Unavailable, "read schema version: %v", err)
	case err != nil:
		have = 0
	}
	if have > SchemaVersion {
		return codes.Errorf(codes.Unavailable,
			"store schema is version %d, this binary knows %d: upgrade the plugin rather than downgrade the store (§5.2)",
			have, SchemaVersion)
	}
	for i := have; i < int64(len(migrations)); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return codes.Errorf(codes.Unavailable, "begin migration %d: %v", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return codes.Errorf(codes.Unavailable, "migration %d: %v", i+1, err)
		}
		if i == 0 {
			_, err = tx.Exec("INSERT INTO meta (schema_version, created_at) VALUES (?, ?)", i+1, nowMS())
		} else {
			_, err = tx.Exec("UPDATE meta SET schema_version = ?", i+1)
		}
		if err != nil {
			tx.Rollback()
			return codes.Errorf(codes.Unavailable, "stamp migration %d: %v", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return codes.Errorf(codes.Unavailable, "commit migration %d: %v", i+1, err)
		}
	}
	return nil
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// nowMS is the clock in Unix milliseconds (§5.3). Callers that can be tested
// pass a timestamp instead of reaching for this.
func nowMS() int64 { return time.Now().UnixMilli() }

// nextSeq allocates the per-project human-friendly number (§5.4).
func nextSeq(tx *sql.Tx, project, entity string) (int64, error) {
	var next int64
	err := tx.QueryRow("SELECT next FROM seqs WHERE project = ? AND entity = ?", project, entity).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		next = 1
	} else if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		"INSERT INTO seqs (project, entity, next) VALUES (?, ?, ?) ON CONFLICT (project, entity) DO UPDATE SET next = ?",
		project, entity, next+1, next+1); err != nil {
		return 0, err
	}
	return next, nil
}

func jsonOrNil(v any) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []tasks.Criterion:
		if len(x) == 0 {
			return nil
		}
	case []string:
		if len(x) == 0 {
			return nil
		}
	case map[string]any:
		if len(x) == 0 {
			return nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	var ce *codes.Error
	if errors.As(err, &ce) {
		return err
	}
	return codes.Errorf(codes.Unexpected, "%v", err)
}

// refClause turns a caller-typed reference into a WHERE fragment: a 26-char
// ULID is identity, a bare integer is the project's seq (§5.4).
func refClause(ref string) (string, any, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil, codes.New(codes.Usage, "an id is required")
	}
	if ids.Valid(ref) {
		return "id = ?", ref, nil
	}
	if n, err := strconv.ParseInt(ref, 10, 64); err == nil && n > 0 {
		return "seq = ?", n, nil
	}
	return "", nil, codes.Errorf(codes.Usage, "%q is neither a 26-character id nor a positive number", ref)
}

func fmtCount(n int) string { return fmt.Sprintf("%d", n) }
