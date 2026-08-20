package store

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/ids"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// Event is one row of an append-only `_events` table, shaped as §8.1's
// payload: {id, at, actor, project, entity, kind, detail}.
type Event struct {
	ID       string          `json:"id"`
	Entity   string          `json:"entity"`
	EntityID string          `json:"entity_id"`
	Project  string          `json:"project"`
	At       int64           `json:"at"`
	Actor    tasks.Principal `json:"actor"`
	Kind     string          `json:"kind"`
	Detail   json.RawMessage `json:"detail,omitempty"`
	// Name is the §8.1 event name, `tasks.<entity>.<verb>`.
	Name string `json:"name"`
}

// EventFilter selects a slice of the trail.
type EventFilter struct {
	Project     string
	AllProjects bool
	Entity      string // "task", "note", or empty for both
	EntityID    string
	SinceID     string
	SinceAt     int64
	Limit       int
}

// appendEvent writes one row of an entity's event table inside the caller's
// transaction — the same transaction as the mutation (§5.5).
func appendEvent(tx *sql.Tx, table, entityID, project string, ev tasks.Event) error {
	var detail any
	if len(ev.Detail) > 0 {
		b, err := json.Marshal(ev.Detail)
		if err != nil {
			return err
		}
		detail = string(b)
	}
	_, err := tx.Exec(
		"INSERT INTO "+table+" (id, entity_id, project, at, actor, kind, detail) VALUES (?, ?, ?, ?, ?, ?, ?)",
		ids.New(ev.At), entityID, project, ev.At, string(ev.Actor), ev.Kind, detail)
	return err
}

// Events reads the merged trail of both entity tables, oldest first. The ULID
// ordering is the stream order, which is what --since resumes from (§8.2).
func (s *Store) Events(f EventFilter) ([]Event, error) {
	parts := []string{}
	args := []any{}
	for _, src := range []struct{ table, entity string }{
		{"tasks_events", "task"}, {"notes_events", "note"},
	} {
		if f.Entity != "" && f.Entity != src.entity {
			continue
		}
		where := []string{"1 = 1"}
		if !f.AllProjects {
			where = append(where, "project = ?")
			args = append(args, f.Project)
		}
		if f.EntityID != "" {
			where = append(where, "entity_id = ?")
			args = append(args, f.EntityID)
		}
		if f.SinceID != "" {
			where = append(where, "id > ?")
			args = append(args, f.SinceID)
		}
		if f.SinceAt > 0 {
			where = append(where, "at > ?")
			args = append(args, f.SinceAt)
		}
		parts = append(parts, "SELECT id, entity_id, project, at, actor, kind, detail, '"+src.entity+
			"' AS entity FROM "+src.table+" WHERE "+strings.Join(where, " AND "))
	}
	if len(parts) == 0 {
		return []Event{}, nil
	}
	q := strings.Join(parts, " UNION ALL ") + " ORDER BY id ASC"
	if f.Limit > 0 {
		q += " LIMIT " + fmtCount(f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var (
			e      Event
			detail sql.NullString
			actor  string
		)
		if err := rows.Scan(&e.ID, &e.EntityID, &e.Project, &e.At, &actor, &e.Kind, &detail, &e.Entity); err != nil {
			return nil, wrap(err)
		}
		e.Actor = tasks.Principal(actor)
		if detail.Valid && detail.String != "" {
			e.Detail = json.RawMessage(detail.String)
		}
		e.Name = "tasks." + e.Entity + "." + e.Kind
		out = append(out, e)
	}
	return out, wrap(rows.Err())
}
