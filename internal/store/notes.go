package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/ids"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

const noteColumns = `id, seq, project, body, status, author, author_name, author_harness,
	verdict, reason, question, task_id, task_project, folded, created_at, updated_at, pane_id`

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
	if _, err := tx.Exec("INSERT INTO notes ("+noteColumns+") VALUES (?"+strings.Repeat(", ?", 16)+")",
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
	if err := updateNote(tx, n); err != nil {
		return nil, wrap(err)
	}
	if err := appendEvent(tx, "notes_events", n.ID, n.Project, ev); err != nil {
		return nil, wrap(err)
	}
	return n, wrap(tx.Commit())
}

// updateNote writes every column of a note back, so one writer cannot forget
// the column another one added.
func updateNote(tx *sql.Tx, n *tasks.Note) error {
	cols := strings.Split(strings.ReplaceAll(noteColumns, "\n\t", " "), ", ")
	sets := make([]string, 0, len(cols))
	for _, c := range cols[1:] {
		sets = append(sets, strings.TrimSpace(c)+" = ?")
	}
	_, err := tx.Exec("UPDATE notes SET "+strings.Join(sets, ", ")+" WHERE id = ?",
		append(noteArgs(n), n.ID)...)
	return err
}

// PromoteNote creates the task and moves the note in ONE transaction, even
// when the two live on different projects: projects are rows in one SQLite
// file, not separate stores, so this is a single atomic write and there is no
// window where a promoted note points at a task that was never created. If
// that ever stops being true — two files, two daemons — this method is where
// the honest two-phase story would have to be written; today it is one BEGIN.
//
// also names further notes on the SAME board whose content is part of this
// task's scope. They are folded into the task this call creates, inside the
// one transaction that creates it: a promote that cannot fold every note it
// was given writes no task at all, rather than leaving the operator to work
// out which half of the request happened.
func (s *Store) PromoteNote(noteProject, ref string, baseUpdatedAt int64, in tasks.NewTaskInput, also []string, by tasks.Actor, now int64) (*tasks.Note, *tasks.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, wrap(err)
	}
	defer tx.Rollback()
	n, err := readNote(tx, noteProject, ref)
	if err != nil {
		return nil, nil, err
	}
	if baseUpdatedAt != 0 && n.UpdatedAt != baseUpdatedAt {
		return nil, nil, codes.Errorf(codes.Conflict,
			"note moved: updated_at is %d, not the %d you read", n.UpdatedAt, baseUpdatedAt)
	}
	in.ID = ids.New(now)
	task, tev, err := tasks.New(in, by, now)
	if err != nil {
		return nil, nil, err
	}
	if task.Seq, err = nextSeq(tx, task.Project, "task"); err != nil {
		return nil, nil, wrap(err)
	}
	if err := insertTask(tx, task); err != nil {
		return nil, nil, wrap(err)
	}
	if err := appendEvent(tx, "tasks_events", task.ID, task.Project, tev); err != nil {
		return nil, nil, wrap(err)
	}
	nev, err := tasks.NotePromote(n, by, task.ID, task.Project, now)
	if err != nil {
		return nil, nil, err
	}
	if err := updateNote(tx, n); err != nil {
		return nil, nil, wrap(err)
	}
	if err := appendEvent(tx, "notes_events", n.ID, n.Project, nev); err != nil {
		return nil, nil, wrap(err)
	}
	for _, ref := range also {
		if _, err := foldInto(tx, noteProject, ref, task, by, now); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, wrap(err)
	}
	return n, task, nil
}

// FoldNote points an existing note at a task that already exists, without
// creating a second one. This is the moment the board's loop produces most
// often: the task is filed, and the note that turns out to be part of it
// arrives afterwards.
//
// taskProject is the board the task lives on, which may not be the note's:
// the same cross-project case PromoteNote already carries.
func (s *Store) FoldNote(noteProject, ref string, baseUpdatedAt int64, taskProject, taskRef string, by tasks.Actor, now int64) (*tasks.Note, *tasks.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, wrap(err)
	}
	defer tx.Rollback()
	if baseUpdatedAt != 0 {
		n, err := readNote(tx, noteProject, ref)
		if err != nil {
			return nil, nil, err
		}
		if n.UpdatedAt != baseUpdatedAt {
			return nil, nil, codes.Errorf(codes.Conflict,
				"note moved: updated_at is %d, not the %d you read", n.UpdatedAt, baseUpdatedAt)
		}
	}
	task, err := readTask(tx, taskProject, taskRef)
	if err != nil {
		return nil, nil, err
	}
	n, err := foldInto(tx, noteProject, ref, task, by, now)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, wrap(err)
	}
	return n, task, nil
}

// foldInto is the one place a fold is written, so a promote that folds and a
// fold that stands alone cannot drift.
func foldInto(tx *sql.Tx, noteProject, ref string, task *tasks.Task, by tasks.Actor, now int64) (*tasks.Note, error) {
	n, err := readNote(tx, noteProject, ref)
	if err != nil {
		return nil, err
	}
	ev, err := tasks.NoteFold(n, by, task.ID, task.Project, holderOf(tx, n), now)
	if err != nil {
		return nil, err
	}
	if err := updateNote(tx, n); err != nil {
		return nil, wrap(err)
	}
	if err := appendEvent(tx, "notes_events", n.ID, n.Project, ev); err != nil {
		return nil, wrap(err)
	}
	return n, nil
}

// holderOf names the task a note is already on, the way an operator would type
// it. The id is the fallback, never a failure: a refusal that cannot name the
// number is still a refusal, and looking the task up must not turn one into an
// error about a different row.
func holderOf(tx *sql.Tx, n *tasks.Note) string {
	if n.TaskID == "" {
		return ""
	}
	board := n.TaskProject
	if board == "" {
		board = n.Project
	}
	t, err := readTask(tx, board, n.TaskID)
	if err != nil {
		return n.TaskID
	}
	return fmt.Sprintf("#%d", t.Seq)
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

// DeleteNote removes a note for good. Only an inbox note qualifies, and only
// its author or the operator (§5.7, §3.1).
func (s *Store) DeleteNote(project, ref string, by tasks.Actor) error {
	tx, err := s.db.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	n, err := readNote(tx, project, ref)
	if err != nil {
		return err
	}
	if err := tasks.CanHardDeleteNote(n, by); err != nil {
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
		taskPrj sql.NullString
		folded  sql.NullBool
		pane    sql.NullString
	)
	if err := sc.Scan(&n.ID, &n.Seq, &n.Project, &n.Body, &n.Status, &n.Author, &name, &harness,
		&verdict, &reason, &quest, &taskID, &taskPrj, &folded, &n.CreatedAt, &n.UpdatedAt, &pane); err != nil {
		return nil, err
	}
	n.AuthorName, n.AuthorHarness = name.String, harness.String
	n.Verdict = tasks.Verdict(verdict.String)
	n.Reason, n.Question, n.TaskID, n.PaneID = reason.String, quest.String, taskID.String, pane.String
	n.TaskProject, n.Folded = taskPrj.String, folded.Bool
	return &n, nil
}

func noteArgs(n *tasks.Note) []any {
	return []any{
		n.Seq, n.Project, n.Body, string(n.Status), string(n.Author),
		nullIfEmpty(n.AuthorName), nullIfEmpty(n.AuthorHarness),
		nullIfEmpty(string(n.Verdict)), nullIfEmpty(n.Reason), nullIfEmpty(n.Question),
		nullIfEmpty(n.TaskID), nullIfEmpty(n.TaskProject), n.Folded, n.CreatedAt, n.UpdatedAt, nullIfEmpty(n.PaneID),
	}
}
