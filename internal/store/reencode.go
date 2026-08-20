package store

import (
	"database/sql"
	"fmt"

	"github.com/husniadil/herdr-tasks/internal/ids"
)

// idColumn is one place a stored id appears: a table, the column, and whether
// it is the row's own identity or a reference to another row's.
type idColumn struct{ table, column string }

// idColumns is every column in this schema that holds an entity id. Getting
// this list wrong is the whole risk of the re-encode, so it is written out
// rather than discovered: a column missed here becomes a reference to a row
// that no longer exists, and a referential check in the migration itself is
// what catches that before the transaction commits.
var idColumns = []idColumn{
	{"tasks", "id"},
	{"notes", "id"},
	{"notes", "task_id"},
	{"parked", "id"},
	{"tasks_events", "id"},
	{"tasks_events", "entity_id"},
	{"notes_events", "id"},
	{"notes_events", "entity_id"},
	{"task_deps", "task_id"},
	{"task_deps", "depends_on_id"},
}

// reencodeIDs rewrites every stored id from the old left-aligned rendering to
// the spec's right-aligned one (§5.4). It runs inside the migration's
// transaction, so either every id moves or none does — a half-migrated store
// would order its trail wrongly at the boundary, because for the same instant
// an old id sorts AFTER a new one.
func reencodeIDs(tx *sql.Tx) error {
	// Foreign keys are on (§5.1) and task_deps references tasks(id) with no
	// ON UPDATE, so rewriting a parent id before its children would be
	// rejected. Deferring the check to COMMIT lets the whole set move at once
	// and still refuses to commit a graph that does not hold together.
	if _, err := tx.Exec("PRAGMA defer_foreign_keys = ON"); err != nil {
		return fmt.Errorf("defer foreign keys: %w", err)
	}
	for _, c := range idColumns {
		if err := reencodeColumn(tx, c); err != nil {
			return err
		}
	}
	return danglingCheck(tx)
}

func reencodeColumn(tx *sql.Tx, c idColumn) error {
	rows, err := tx.Query(fmt.Sprintf(
		"SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL AND %s != ''", c.column, c.table, c.column, c.column))
	if err != nil {
		return fmt.Errorf("read %s.%s: %w", c.table, c.column, err)
	}
	old := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan %s.%s: %w", c.table, c.column, err)
		}
		old = append(old, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s.%s: %w", c.table, c.column, err)
	}
	for _, v := range old {
		next, ok := ids.Reencode(v)
		if !ok {
			// Not a 26-character id at all. Leaving it alone is the only safe
			// answer: it was not minted by the encoder this is correcting.
			continue
		}
		if next == v {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", c.table, c.column, c.column),
			next, v); err != nil {
			return fmt.Errorf("rewrite %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// danglingCheck refuses to commit a graph that does not hold together. It is
// the criterion "no row references an id that no longer exists", asserted by
// the migration itself rather than only by a test.
func danglingCheck(tx *sql.Tx) error {
	for _, ref := range []struct{ table, column, parent string }{
		{"task_deps", "task_id", "tasks"},
		{"task_deps", "depends_on_id", "tasks"},
		{"tasks_events", "entity_id", "tasks"},
		{"notes_events", "entity_id", "notes"},
	} {
		var n int
		if err := tx.QueryRow(fmt.Sprintf(
			"SELECT COUNT(*) FROM %s r WHERE r.%s IS NOT NULL AND r.%s != '' AND NOT EXISTS (SELECT 1 FROM %s p WHERE p.id = r.%s)",
			ref.table, ref.column, ref.column, ref.parent, ref.column)).Scan(&n); err != nil {
			return fmt.Errorf("check %s.%s: %w", ref.table, ref.column, err)
		}
		if n > 0 {
			return fmt.Errorf("re-encoding left %d %s.%s rows pointing at no %s row",
				n, ref.table, ref.column, ref.parent)
		}
	}
	return nil
}
