package store

import "github.com/husniadil/herdr-tasks/internal/tasks"

// Dump is the whole store as plain data (§5.8): a plugin whose data cannot be
// read without the plugin is not acceptable, so this holds every table, not a
// summary.
type Dump struct {
	SchemaVersion int64         `json:"schema_version"`
	CreatedAt     int64         `json:"created_at"`
	Tasks         []*tasks.Task `json:"tasks"`
	Notes         []*tasks.Note `json:"notes"`
	Events        []Event       `json:"events"`
	Parked        []Parked      `json:"parked"`
	Deps          []DepEdge     `json:"deps"`
	Seqs          []SeqCounter  `json:"seqs"`
}

// DepEdge is one edge of the dependency DAG.
type DepEdge struct {
	TaskID      string `json:"task_id"`
	DependsOnID string `json:"depends_on_id"`
}

// SeqCounter is one project's next human-friendly number (§5.4).
type SeqCounter struct {
	Project string `json:"project"`
	Entity  string `json:"entity"`
	Next    int64  `json:"next"`
}

// Dump reads everything, across every project.
func (s *Store) Dump() (*Dump, error) {
	d := &Dump{}
	if err := s.db.QueryRow("SELECT schema_version, created_at FROM meta").
		Scan(&d.SchemaVersion, &d.CreatedAt); err != nil {
		return nil, wrap(err)
	}
	var err error
	if d.Tasks, err = s.ListTasks(TaskFilter{AllProjects: true, Archived: true}); err != nil {
		return nil, err
	}
	if d.Notes, err = s.ListNotes(NoteFilter{AllProjects: true}); err != nil {
		return nil, err
	}
	if d.Events, err = s.Events(EventFilter{AllProjects: true}); err != nil {
		return nil, err
	}
	d.Parked = []Parked{}
	rows, err := s.db.Query(
		`SELECT id, project, subject, verb, target, payload, state, COALESCE(reason, ''), created_at,
		        COALESCE(resolved_at, 0), COALESCE(resolved_by, '') FROM parked ORDER BY id ASC`)
	if err != nil {
		return nil, wrap(err)
	}
	for rows.Next() {
		var p Parked
		if err := rows.Scan(&p.ID, &p.Project, &p.Subject, &p.Verb, &p.Target, &p.Payload,
			&p.State, &p.Reason, &p.CreatedAt, &p.ResolvedAt, &p.ResolvedBy); err != nil {
			rows.Close()
			return nil, wrap(err)
		}
		d.Parked = append(d.Parked, p)
	}
	rows.Close()

	d.Deps = []DepEdge{}
	rows, err = s.db.Query("SELECT task_id, depends_on_id FROM task_deps ORDER BY task_id, depends_on_id")
	if err != nil {
		return nil, wrap(err)
	}
	for rows.Next() {
		var e DepEdge
		if err := rows.Scan(&e.TaskID, &e.DependsOnID); err != nil {
			rows.Close()
			return nil, wrap(err)
		}
		d.Deps = append(d.Deps, e)
	}
	rows.Close()

	d.Seqs = []SeqCounter{}
	rows, err = s.db.Query("SELECT project, entity, next FROM seqs ORDER BY project, entity")
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c SeqCounter
		if err := rows.Scan(&c.Project, &c.Entity, &c.Next); err != nil {
			return nil, wrap(err)
		}
		d.Seqs = append(d.Seqs, c)
	}
	return d, wrap(rows.Err())
}
