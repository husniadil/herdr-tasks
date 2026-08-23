package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/ids"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

const taskColumns = `id, seq, project, title, description, status, priority, validation,
	discovered_from, created_by, created_at, updated_at, claimed_by, claimed_by_name,
	claimed_by_harness, claimed_by_session, claimed_at, lease_until, ever_claimed,
	release_note, released_at, report, evidence, evidence_for, submitted_by, submitted_by_harness,
	submitted_by_session, submitted_at, feedback, reviewed_by, completed_at, cancelled_at, archived_at,
	pane_id, tab_id, workspace_id, amended_at, amend_count`

// CreateTask allocates an id and a seq and writes the task with its created
// event, in one transaction (§5.5).
func (s *Store) CreateTask(in tasks.NewTaskInput, by tasks.Actor, now int64) (*tasks.Task, error) {
	in.ID = ids.New(now)
	task, ev, err := tasks.New(in, by, now)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	if task.Seq, err = nextSeq(tx, task.Project, "task"); err != nil {
		return nil, wrap(err)
	}
	if err := validateDeps(tx, task.ID, task.Project, task.Deps); err != nil {
		return nil, err
	}
	if err := insertTask(tx, task); err != nil {
		return nil, wrap(err)
	}
	if err := setDeps(tx, task.ID, task.Deps); err != nil {
		return nil, wrap(err)
	}
	if err := appendEvent(tx, "tasks_events", task.ID, task.Project, ev); err != nil {
		return nil, wrap(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, wrap(err)
	}
	return task, nil
}

// TaskTransition loads a task, hands it to a state-machine transition, and
// writes the result and its event in one transaction. baseUpdatedAt, when
// non-zero, is the caller's --base-updated-at guard: a row that moved is
// CONFLICT, never a silent overwrite (§5.6).
func (s *Store) TaskTransition(project, ref string, baseUpdatedAt int64, fn func(*tasks.Task) (tasks.Event, error)) (*tasks.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	task, err := readTask(tx, project, ref)
	if err != nil {
		return nil, err
	}
	if baseUpdatedAt != 0 && task.UpdatedAt != baseUpdatedAt {
		return nil, codes.Errorf(codes.Conflict,
			"task moved: updated_at is %d, not the %d you read", task.UpdatedAt, baseUpdatedAt)
	}
	before := append([]string(nil), task.Deps...)
	ev, err := fn(task)
	if err != nil {
		return nil, err
	}
	if !sameSet(before, task.Deps) {
		if err := validateDeps(tx, task.ID, task.Project, task.Deps); err != nil {
			return nil, err
		}
		if err := setDeps(tx, task.ID, task.Deps); err != nil {
			return nil, wrap(err)
		}
	}
	if err := updateTask(tx, task); err != nil {
		return nil, wrap(err)
	}
	if err := appendEvent(tx, "tasks_events", task.ID, task.Project, ev); err != nil {
		return nil, wrap(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, wrap(err)
	}
	task.Blocked, task.Abandoned, _ = blockedBy(s.db, task.ID)
	return task, nil
}

// GetTask reads one task by ULID or by the project's seq.
func (s *Store) GetTask(project, ref string) (*tasks.Task, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	return readTask(tx, project, ref)
}

// GetTaskAnyProject reads one task by ULID, whichever board it was filed on
// (§4.4). Only a ULID addresses a task across boards: a seq is minted per
// project, so the same number exists on several boards at once and resolving
// one of them would be a guess.
func (s *Store) GetTaskAnyProject(ref string) (*tasks.Task, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, codes.New(codes.Usage, "an id is required")
	}
	if !ids.Valid(ref) {
		return nil, codes.Errorf(codes.Usage,
			"%q is not a 26-character id: a number is only unique inside a project, so it cannot address a task across projects", ref)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, wrap(err)
	}
	defer tx.Rollback()
	row := tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE id = ?", ref)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, codes.Errorf(codes.NotFound, "no task %s in any project", ref)
	}
	if err != nil {
		return nil, wrap(err)
	}
	if t.Deps, err = readDeps(tx, t.ID); err != nil {
		return nil, wrap(err)
	}
	if t.Blocked, t.Abandoned, err = blockedBy(tx, t.ID); err != nil {
		return nil, err
	}
	return t, nil
}

// TaskFilter is what list verbs accept. Project is the default scope;
// AllProjects is §4.4's opt-out.
type TaskFilter struct {
	Project     string
	AllProjects bool
	Status      string
	Ready       bool
	Mine        tasks.Principal
	Query       string
	Archived    bool
	// Elsewhere inverts the project scope: every project EXCEPT Project. It
	// answers "is the operator looking at the wrong board" with the same
	// clauses the list itself used, so the count can never disagree with what
	// the command it suggests would print.
	Elsewhere bool
	Limit     int
}

// ListTasks returns tasks in the filter's scope, newest last.
func (s *Store) ListTasks(f TaskFilter) ([]*tasks.Task, error) {
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
	if f.Mine != "" {
		where, args = append(where, "claimed_by = ?"), append(args, string(f.Mine))
	}
	if f.Query != "" {
		// A caller typing "50%" means the characters, not "anything after 50".
		q := "%" + likeEscape(f.Query) + "%"
		where = append(where, `(title LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\')`)
		args = append(args, q, q)
	}
	if !f.Archived {
		where = append(where, "archived_at IS NULL")
	}
	q := "SELECT " + taskColumns + " FROM tasks WHERE " + strings.Join(where, " AND ") +
		" ORDER BY priority DESC, seq ASC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []*tasks.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, wrap(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(err)
	}
	if err := s.fillDeps(out); err != nil {
		return nil, err
	}
	if f.Ready {
		ready := make([]*tasks.Task, 0, len(out))
		for _, t := range out {
			if tasks.Ready(t) {
				ready = append(ready, t)
			}
		}
		out = ready
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// DeleteTask removes a task for good. Only a never-claimed one qualifies
// (§5.7); everything else is cancelled or archived.
func (s *Store) DeleteTask(project, ref string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	task, err := readTask(tx, project, ref)
	if err != nil {
		return err
	}
	if err := tasks.CanHardDelete(task); err != nil {
		return err
	}
	// §5.7 stops at "never claimed", which is exactly what a placeholder for
	// future work looks like — and task_deps cascades on delete, so removing
	// one took the edge with it and its dependents quietly became ready, with
	// nothing in their trail saying why. A task others are waiting on is not
	// this caller's alone to remove: they drop the edge first, deliberately.
	held, err := dependentSeqs(tx, task.ID)
	if err != nil {
		return err
	}
	if len(held) > 0 {
		return codes.Errorf(codes.Conflict,
			"%s is a dependency of %s; drop the edge with `task update --depends-on` before deleting it",
			refOf(task), strings.Join(held, ", "))
	}
	// The ROW goes; the trail stays. §5.5 calls tasks_events append-only, and
	// erasing it here made the one operation the trail most needed to survive
	// the one that removed it: a task created and then deleted left no record
	// that either had happened. Nothing joins an event to a live entity — the
	// event carries its own entity_id, not a foreign key — so the rows keep
	// reading after the task they name has gone.
	if _, err := tx.Exec("DELETE FROM tasks WHERE id = ?", task.ID); err != nil {
		return wrap(err)
	}
	return wrap(tx.Commit())
}

// SweepLeases releases every claim whose lease has elapsed and records the
// sweep in the task's events (§11.5). It returns the ids it released.
func (s *Store) SweepLeases(now int64) ([]string, error) {
	rows, err := s.db.Query(
		"SELECT id, project FROM tasks WHERE claimed_by IS NOT NULL AND claimed_by != '' AND lease_until > 0 AND lease_until < ?", now)
	if err != nil {
		return nil, wrap(err)
	}
	type ref struct{ id, project string }
	stale := []ref{}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.project); err != nil {
			rows.Close()
			return nil, wrap(err)
		}
		stale = append(stale, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, wrap(err)
	}
	swept := make([]string, 0, len(stale))
	for _, r := range stale {
		if s.DuringSweep != nil {
			s.DuringSweep(r.id)
		}
		_, err := s.TaskTransition(r.project, r.id, 0, func(t *tasks.Task) (tasks.Event, error) {
			// The scan and this transition are separate statements, and the
			// holder can touch in between. Release with KindSwept skips the
			// holder check on purpose (tasks/task.go:245), so this re-read is
			// the only thing standing between a renewed lease and being
			// handed back to the queue underneath its holder.
			if !tasks.LeaseExpired(t, now) {
				return tasks.Event{}, codes.Errorf(codes.Conflict,
					"the lease on %s was renewed after the scan", r.id)
			}
			// Not t.ReleaseNote: that belongs to whoever released this task
			// last, which is not the claimer being swept now.
			return tasks.Release(t, tasks.Actor{Principal: tasks.PrincipalPlugin}, "lease expired", now, tasks.KindSwept)
		})
		if err != nil {
			// Another writer got there first; the sweep is best-effort by
			// design and says nothing it did not do.
			continue
		}
		swept = append(swept, r.id)
	}
	return swept, nil
}

// ReleaseByPane releases every claim held by a pane, which is what a
// pane.exited event means for a lease (§11.5).
func (s *Store) ReleaseByPane(paneID string, now int64) ([]string, error) {
	principal := "agent:" + paneID
	// lease_until > 0 is the same reach the timer sweep has (SweepLeases): a
	// dead pane gives back the lease it was holding, and submit already ended
	// that lease. A row still carrying this pane in claimed_by with no lease
	// left was handed off, not abandoned.
	rows, err := s.db.Query(
		"SELECT id, project FROM tasks WHERE claimed_by = ? AND lease_until > 0", principal)
	if err != nil {
		return nil, wrap(err)
	}
	type ref struct{ id, project string }
	held := []ref{}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.project); err != nil {
			rows.Close()
			return nil, wrap(err)
		}
		held = append(held, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, wrap(err)
	}
	rows.Close()
	out := make([]string, 0, len(held))
	for _, r := range held {
		if s.DuringSweep != nil {
			s.DuringSweep(r.id)
		}
		if _, err := s.TaskTransition(r.project, r.id, 0, func(t *tasks.Task) (tasks.Event, error) {
			// Same window as SweepLeases: a task another pane claimed since
			// the scan is not this pane's to give back, and one this pane
			// submitted since the scan no longer has a lease to give.
			if t.ClaimedBy != tasks.Principal(principal) {
				return tasks.Event{}, codes.Errorf(codes.Conflict,
					"%s is claimed by %s, not the pane that exited", r.id, t.ClaimedBy)
			}
			if t.LeaseUntil == 0 {
				return tasks.Event{}, codes.Errorf(codes.Conflict,
					"the lease on %s ended after the scan", r.id)
			}
			return tasks.Release(t, tasks.Actor{Principal: tasks.PrincipalPlugin}, "pane exited", now, tasks.KindSwept)
		}); err == nil {
			out = append(out, r.id)
		}
	}
	return out, nil
}

// Dependents returns the tasks that depend on this one.
func (s *Store) Dependents(id string) ([]string, error) {
	rows, err := s.db.Query("SELECT task_id FROM task_deps WHERE depends_on_id = ? ORDER BY task_id", id)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, wrap(err)
		}
		out = append(out, v)
	}
	return out, wrap(rows.Err())
}

// blockedBy answers both halves of §5.8 in one read: whether anything this
// task depends on is unfinished, and which of those were CANCELLED — a
// dependency that will never be done, so its dependent is blocked until the
// operator edits the edge. It is the ONE place that answers this, because two
// places computing the same predicate is how they come apart.
func blockedBy(q querier, id string) (bool, []int64, error) {
	rows, err := q.Query(
		`SELECT t.status, t.seq FROM task_deps d JOIN tasks t ON t.id = d.depends_on_id
		 WHERE d.task_id = ? AND t.status != 'done' ORDER BY t.seq ASC`, id)
	if err != nil {
		return false, nil, wrap(err)
	}
	defer rows.Close()
	blocked := false
	abandoned := []int64{}
	for rows.Next() {
		var status string
		var seq int64
		if err := rows.Scan(&status, &seq); err != nil {
			return false, nil, wrap(err)
		}
		blocked = true
		if status == string(tasks.StatusCancelled) {
			abandoned = append(abandoned, seq)
		}
	}
	if err := rows.Err(); err != nil {
		return false, nil, wrap(err)
	}
	if len(abandoned) == 0 {
		abandoned = nil
	}
	return blocked, abandoned, nil
}

func (s *Store) fillDeps(list []*tasks.Task) error {
	for _, t := range list {
		deps, err := readDeps(s.db, t.ID)
		if err != nil {
			return wrap(err)
		}
		t.Deps = deps
		blocked, abandoned, err := blockedBy(s.db, t.ID)
		if err != nil {
			return err
		}
		t.Blocked, t.Abandoned = blocked, abandoned
	}
	return nil
}

type querier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func readDeps(q querier, id string) ([]string, error) {
	rows, err := q.Query("SELECT depends_on_id FROM task_deps WHERE task_id = ? ORDER BY depends_on_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func readTask(tx *sql.Tx, project, ref string) (*tasks.Task, error) {
	clause, arg, err := refClause(ref)
	if err != nil {
		return nil, err
	}
	row := tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE project = ? AND "+clause, project, arg)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, codes.Errorf(codes.NotFound, "no task %s in %s", ref, project)
	}
	if err != nil {
		return nil, wrap(err)
	}
	if t.Deps, err = readDeps(tx, t.ID); err != nil {
		return nil, wrap(err)
	}
	if t.Blocked, t.Abandoned, err = blockedBy(tx, t.ID); err != nil {
		return nil, err
	}
	return t, nil
}

type scanner interface{ Scan(...any) error }

func scanTask(sc scanner) (*tasks.Task, error) {
	var (
		t            tasks.Task
		desc         sql.NullString
		validation   sql.NullString
		discovered   sql.NullString
		claimedBy    sql.NullString
		claimName    sql.NullString
		claimHarness sql.NullString
		claimSession sql.NullString
		claimedAt    sql.NullInt64
		leaseUntil   sql.NullInt64
		everClaimed  int64
		releaseNote  sql.NullString
		releasedAt   sql.NullInt64
		report       sql.NullString
		evidence     sql.NullString
		evidenceFor  sql.NullString
		submittedBy  sql.NullString
		submitHarn   sql.NullString
		submitSess   sql.NullString
		submittedAt  sql.NullInt64
		feedback     sql.NullString
		reviewedBy   sql.NullString
		completedAt  sql.NullInt64
		cancelledAt  sql.NullInt64
		archivedAt   sql.NullInt64
		pane         sql.NullString
		tab          sql.NullString
		ws           sql.NullString
		amendedAt    sql.NullInt64
		amendCount   sql.NullInt64
	)
	if err := sc.Scan(&t.ID, &t.Seq, &t.Project, &t.Title, &desc, &t.Status, &t.Priority, &validation,
		&discovered, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &claimedBy, &claimName,
		&claimHarness, &claimSession, &claimedAt, &leaseUntil, &everClaimed,
		&releaseNote, &releasedAt, &report, &evidence, &evidenceFor, &submittedBy, &submitHarn,
		&submitSess, &submittedAt, &feedback, &reviewedBy, &completedAt, &cancelledAt, &archivedAt,
		&pane, &tab, &ws, &amendedAt, &amendCount); err != nil {
		return nil, err
	}
	t.Description = desc.String
	// Fail loud: a column this store wrote as JSON that does not read back as
	// JSON is a corrupt row, and swallowing it hands the caller a task whose
	// criteria or evidence silently vanished.
	for _, col := range []struct {
		name string
		raw  sql.NullString
		into any
	}{
		{"validation", validation, &t.Validation},
		{"evidence", evidence, &t.Evidence},
		{"evidence_for", evidenceFor, &t.EvidenceFor},
	} {
		if !col.raw.Valid || col.raw.String == "" {
			continue
		}
		if err := json.Unmarshal([]byte(col.raw.String), col.into); err != nil {
			return nil, codes.Errorf(codes.Unexpected,
				"task %s: %s is not readable JSON: %v", t.ID, col.name, err)
		}
	}
	t.DiscoveredFrom = discovered.String
	t.ClaimedBy = tasks.Principal(claimedBy.String)
	t.ClaimedByName, t.ClaimedByHarness, t.ClaimedBySession = claimName.String, claimHarness.String, claimSession.String
	t.ClaimedAt, t.LeaseUntil = claimedAt.Int64, leaseUntil.Int64
	t.EverClaimed = everClaimed == 1
	t.ReleaseNote, t.ReleasedAt = releaseNote.String, releasedAt.Int64
	t.Report = report.String
	t.SubmittedBy, t.SubmittedByHarness, t.SubmittedAt = tasks.Principal(submittedBy.String), submitHarn.String, submittedAt.Int64
	t.SubmittedBySession = submitSess.String
	t.Feedback, t.ReviewedBy = feedback.String, tasks.Principal(reviewedBy.String)
	t.CompletedAt, t.CancelledAt, t.ArchivedAt = completedAt.Int64, cancelledAt.Int64, archivedAt.Int64
	t.PaneID, t.TabID, t.WorkspaceID = pane.String, tab.String, ws.String
	t.AmendedAt, t.AmendCount = amendedAt.Int64, amendCount.Int64
	return &t, nil
}

func taskArgs(t *tasks.Task) []any {
	return []any{
		t.Seq, t.Project, t.Title, t.Description, string(t.Status), t.Priority,
		jsonOrNil(t.Validation), nullIfEmpty(t.DiscoveredFrom), string(t.CreatedBy),
		t.CreatedAt, t.UpdatedAt, nullIfEmpty(string(t.ClaimedBy)), nullIfEmpty(t.ClaimedByName),
		nullIfEmpty(t.ClaimedByHarness), nullIfEmpty(t.ClaimedBySession),
		nullIfZero(t.ClaimedAt), nullIfZero(t.LeaseUntil),
		boolInt(t.EverClaimed), nullIfEmpty(t.ReleaseNote), nullIfZero(t.ReleasedAt), nullIfEmpty(t.Report),
		jsonOrNil(t.Evidence), jsonOrNil(t.EvidenceFor), nullIfEmpty(string(t.SubmittedBy)), nullIfEmpty(t.SubmittedByHarness),
		nullIfEmpty(t.SubmittedBySession), nullIfZero(t.SubmittedAt), nullIfEmpty(t.Feedback), nullIfEmpty(string(t.ReviewedBy)),
		nullIfZero(t.CompletedAt), nullIfZero(t.CancelledAt), nullIfZero(t.ArchivedAt),
		nullIfEmpty(t.PaneID), nullIfEmpty(t.TabID), nullIfEmpty(t.WorkspaceID),
		nullIfZero(t.AmendedAt), nullIfZero(t.AmendCount),
	}
}

func insertTask(tx *sql.Tx, t *tasks.Task) error {
	args := append([]any{t.ID}, taskArgs(t)...)
	_, err := tx.Exec("INSERT INTO tasks ("+taskColumns+") VALUES (?"+strings.Repeat(", ?", len(args)-1)+")", args...)
	return err
}

func updateTask(tx *sql.Tx, t *tasks.Task) error {
	cols := strings.Split(strings.ReplaceAll(taskColumns, "\n\t", " "), ", ")
	sets := make([]string, 0, len(cols))
	for _, c := range cols[1:] {
		sets = append(sets, strings.TrimSpace(c)+" = ?")
	}
	_, err := tx.Exec("UPDATE tasks SET "+strings.Join(sets, ", ")+" WHERE id = ?",
		append(taskArgs(t), t.ID)...)
	return err
}

// validateDeps enforces the DAG rules: a dep exists, lives in the same project
// (§4.4), and does not close a cycle.
func validateDeps(tx *sql.Tx, taskID, project string, deps []string) error {
	if len(deps) == 0 {
		return nil
	}
	for _, d := range deps {
		var depProject string
		err := tx.QueryRow("SELECT project FROM tasks WHERE id = ?", d).Scan(&depProject)
		if errors.Is(err, sql.ErrNoRows) {
			return codes.Errorf(codes.NotFound, "no such dependency: %s", d)
		}
		if err != nil {
			return wrap(err)
		}
		if depProject != project {
			return codes.Errorf(codes.Usage, "dependency %s is in another project (§4.4)", d)
		}
	}
	edges := map[string][]string{}
	rows, err := tx.Query("SELECT task_id, depends_on_id FROM task_deps WHERE task_id != ?", taskID)
	if err != nil {
		return wrap(err)
	}
	defer rows.Close()
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return wrap(err)
		}
		edges[from] = append(edges[from], to)
	}
	if err := rows.Err(); err != nil {
		return wrap(err)
	}
	return tasks.CheckCycle(taskID, deps, edges)
}

func setDeps(tx *sql.Tx, taskID string, deps []string) error {
	if _, err := tx.Exec("DELETE FROM task_deps WHERE task_id = ?", taskID); err != nil {
		return err
	}
	for _, d := range deps {
		if _, err := tx.Exec("INSERT OR IGNORE INTO task_deps (task_id, depends_on_id) VALUES (?, ?)", taskID, d); err != nil {
			return err
		}
	}
	return nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// likeEscape makes LIKE's two wildcards literal, under the ESCAPE clause the
// query above declares.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullIfZero keeps an unset timestamp NULL rather than 0, so `archived_at IS
// NULL` means what it reads as (§5.3 fixes the unit, not a sentinel).
func nullIfZero(ms int64) any {
	if ms == 0 {
		return nil
	}
	return ms
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// dependentSeqs names the tasks that depend on this one, the way an operator
// would type them.
func dependentSeqs(q querier, id string) ([]string, error) {
	rows, err := q.Query(
		`SELECT t.seq FROM task_deps d JOIN tasks t ON t.id = d.task_id
		 WHERE d.depends_on_id = ? ORDER BY t.seq ASC`, id)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return nil, wrap(err)
		}
		out = append(out, "#"+strconv.FormatInt(seq, 10))
	}
	return out, wrap(rows.Err())
}

// refOf is how a task is named in a message: its number, which is what a human
// types (§5.4).
func refOf(t *tasks.Task) string { return "#" + strconv.FormatInt(t.Seq, 10) }
