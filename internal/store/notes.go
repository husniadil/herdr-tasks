package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/ids"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

const noteColumns = `id, seq, project, body, status, author, author_name, author_harness,
	verdict, reason, question, task_id, created_at, updated_at, pane_id`

// CreateNote files a note with its added event, in one transaction.
func (s *Store) CreateNote(in tasks.NewNoteInput, by tasks.Actor, now int64) (*tasks.Note, error) {
	in.ID = ids.New(now)
	n, ev, err := tasks.NewNote(in, by, now)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	if n.Seq, err = nextSeq(tx, n.Project, "note"); err != nil {
		return nil, wrap(err)
	}
	if _, err := tx.Exec("INSERT INTO notes ("+noteColumns+") VALUES (?"+strings.Repeat(", ?", 14)+")",
		append([]any{n.ID}, noteArgs(n)...)...); err != nil {
		return nil, wrap(err)
	}
	if err := appendEvent(tx, "notes_events", n.ID, n.Project, ev); err != nil {
		return nil, wrap(err)
	}
	return n, wrap(tx.Commit())
}

// NoteTransition is the note counterpart of TaskTransition (§5.5, §5.6).
func (s *Store) NoteTransition(project, ref string, baseUpdatedAt int64, fn func(*tasks.Note) (tasks.Event, error)) (*tasks.Note, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	n, err := readNote(tx, project, ref)
	if err != nil {
		return nil, err
	}
	if baseUpdatedAt != 0 && n.UpdatedAt != baseUpdatedAt {
		return nil, codes.Errorf(codes.Conflict,
			"note moved: updated_at is %d, not the %d you read", n.UpdatedAt, baseUpdatedAt)
	}
	ev, err := fn(n)
	if err != nil {
		return nil, err
	}
	cols := strings.Split(strings.ReplaceAll(noteColumns, "\n\t", " "), ", ")
	sets := make([]string, 0, len(cols))
	for _, c := range cols[1:] {
		sets = append(sets, strings.TrimSpace(c)+" = ?")
	}
	if _, err := tx.Exec("UPDATE notes SET "+strings.Join(sets, ", ")+" WHERE id = ?",
		append(noteArgs(n), n.ID)...); err != nil {
		return nil, wrap(err)
	}
	if err := appendEvent(tx, "notes_events", n.ID, n.Project, ev); err != nil {
		return nil, wrap(err)
	}
	return n, wrap(tx.Commit())
}

// GetNote reads one note by ULID or by the project's seq.
func (s *Store) GetNote(project, ref string) (*tasks.Note, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	return readNote(tx, project, ref)
}

// NoteFilter is what `note list` accepts.
type NoteFilter struct {
	Project     string
	AllProjects bool
	Status      string
	Query       string
	// Elsewhere inverts the project scope: every project EXCEPT Project.
	Elsewhere bool
	Limit     int
}

// ListNotes returns notes in the filter's scope.
func (s *Store) ListNotes(f NoteFilter) ([]*tasks.Note, error) {
	where := []string{"1 = 1"}
	args := []any{}
	switch {
	case f.AllProjects:
	case f.Elsewhere:
		where, args = append(where, "project != ?"), append(args, f.Project)
	default:
		where, args = append(where, "project = ?"), append(args, f.Project)
	}
	if f.Status != "" {
		where, args = append(where, "status = ?"), append(args, f.Status)
	}
	if f.Query != "" {
		// The same match `task list --query` makes over a task's free text: a
		// substring, with % and _ escaped so a caller typing "50%" means the
		// characters. A note's free text is its body and the verdict reason.
		q := "%" + likeEscape(f.Query) + "%"
		where = append(where, `(body LIKE ? ESCAPE '\' OR reason LIKE ? ESCAPE '\')`)
		args = append(args, q, q)
	}
	rows, err := s.db.Query("SELECT "+noteColumns+" FROM notes WHERE "+strings.Join(where, " AND ")+" ORDER BY seq ASC", args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []*tasks.Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, wrap(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(err)
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// DeleteNote removes a note for good. Only an inbox note qualifies (§5.7).
func (s *Store) DeleteNote(project, ref string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	n, err := readNote(tx, project, ref)
	if err != nil {
		return err
	}
	if err := tasks.CanHardDeleteNote(n); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM notes WHERE id = ?", n.ID); err != nil {
		return wrap(err)
	}
	if _, err := tx.Exec("DELETE FROM notes_events WHERE entity_id = ?", n.ID); err != nil {
		return wrap(err)
	}
	return wrap(tx.Commit())
}

func readNote(tx *sql.Tx, project, ref string) (*tasks.Note, error) {
	clause, arg, err := refClause(ref)
	if err != nil {
		return nil, err
	}
	n, err := scanNote(tx.QueryRow("SELECT "+noteColumns+" FROM notes WHERE project = ? AND "+clause, project, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, codes.Errorf(codes.NotFound, "no note %s in %s", ref, project)
	}
	return n, wrap(err)
}

func scanNote(sc scanner) (*tasks.Note, error) {
	var (
		n       tasks.Note
		name    sql.NullString
		harness sql.NullString
		verdict sql.NullString
		reason  sql.NullString
		quest   sql.NullString
		taskID  sql.NullString
		pane    sql.NullString
	)
	if err := sc.Scan(&n.ID, &n.Seq, &n.Project, &n.Body, &n.Status, &n.Author, &name, &harness,
		&verdict, &reason, &quest, &taskID, &n.CreatedAt, &n.UpdatedAt, &pane); err != nil {
		return nil, err
	}
	n.AuthorName, n.AuthorHarness = name.String, harness.String
	n.Verdict = tasks.Verdict(verdict.String)
	n.Reason, n.Question, n.TaskID, n.PaneID = reason.String, quest.String, taskID.String, pane.String
	return &n, nil
}

func noteArgs(n *tasks.Note) []any {
	return []any{
		n.Seq, n.Project, n.Body, string(n.Status), string(n.Author),
		nullIfEmpty(n.AuthorName), nullIfEmpty(n.AuthorHarness),
		nullIfEmpty(string(n.Verdict)), nullIfEmpty(n.Reason), nullIfEmpty(n.Question),
		nullIfEmpty(n.TaskID), n.CreatedAt, n.UpdatedAt, nullIfEmpty(n.PaneID),
	}
}
