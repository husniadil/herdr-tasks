package store

import (
	"database/sql"
	"errors"

	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/ids"
	"github.com/husniadil/herdr-tasks/internal/tasks"
)

// Event kinds of the parked queue. They live here rather than in
// internal/tasks because the parked queue has no state machine there: it is a
// store entity, and these are exactly the four states its rows move through.
const (
	KindParked   = "parked"
	KindResolved = "resolved"
	KindRejected = "rejected"
	KindFailed   = "failed"
)

// Parked is a verb the policy gate deferred: recorded, not performed, waiting
// for the operator (§9.3).
type Parked struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Subject string `json:"subject"`
	Verb    string `json:"verb"`
	Target  string `json:"target"`
	Payload string `json:"payload"`
	// TabID, WorkspaceID and AllProjects are what the DOOR derived rather
	// than what the caller passed, and §9.3 re-runs the verb as the original
	// call — so a re-run without them files the row differently from the one
	// the gate deferred, or looks for it on the wrong board.
	TabID       string `json:"tab_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AllProjects bool   `json:"all_projects,omitempty"`
	// BaseUpdatedAt is the §5.6 guard the deferred call carried, re-applied
	// on the re-run so a resolve cannot overwrite what moved in between.
	BaseUpdatedAt int64  `json:"base_updated_at,omitempty"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	// Error is why the verb failed when the operator resolved it. A parked
	// action that was decided and did not happen is not a resolved one, and
	// the operator needs to see which.
	Error string `json:"error,omitempty"`
	// ResolvedBy is the principal that ran or rejected this action. §9.3
	// re-runs the verb under the ORIGINAL subject, so without this the trail
	// shows the deferred agent acting and nothing about who decided it could
	// — which is the whole accountability once §3.7 made the operator's
	// authority advice rather than a refusal.
	ResolvedBy string `json:"resolved_by,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	ResolvedAt int64  `json:"resolved_at,omitempty"`
}

// Park records a deferred action and returns its id, which the caller hands
// back in the DENIED error as parked_id (§9.3).
func (s *Store) Park(p Parked, now int64) (string, error) {
	id := ids.New(now)
	if p.Payload == "" {
		p.Payload = "{}"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", wrap(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO parked (id, project, subject, verb, target, payload, state, reason, created_at,
		                     tab_id, workspace_id, all_projects, base_updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'parked', ?, ?, ?, ?, ?, ?)`,
		id, p.Project, p.Subject, p.Verb, p.Target, p.Payload, nullIfEmpty(p.Reason), now,
		nullIfEmpty(p.TabID), nullIfEmpty(p.WorkspaceID), p.AllProjects, p.BaseUpdatedAt); err != nil {
		return "", wrap(err)
	}
	// The actor is the subject the gate stopped: the deferral is a fact about
	// that principal's call, and no one else has acted yet.
	if err := appendEvent(tx, "parked_events", id, p.Project, tasks.Event{
		Kind: KindParked, Actor: tasks.Principal(p.Subject), At: now,
		Detail: map[string]any{"verb": p.Verb, "target": p.Target, "reason": p.Reason},
	}); err != nil {
		return "", wrap(err)
	}
	return id, wrap(tx.Commit())
}

// ListParked returns the actions in a project that still want the operator's
// attention: the ones still waiting, and the ones they resolved whose verb
// then failed. A failed action is not finished business — hiding it would make
// a verb that did not happen look like one that did.
func (s *Store) ListParked(project string) ([]Parked, error) {
	rows, err := s.db.Query(
		`SELECT id, project, subject, verb, target, payload, state, COALESCE(reason, ''),
		        COALESCE(error, ''), COALESCE(resolved_by, ''), created_at,
		        COALESCE(tab_id, ''), COALESCE(workspace_id, ''), COALESCE(all_projects, 0),
		        COALESCE(base_updated_at, 0)
		 FROM parked WHERE project = ? AND state IN ('parked', 'failed') ORDER BY id ASC`, project)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []Parked{}
	for rows.Next() {
		var p Parked
		if err := rows.Scan(&p.ID, &p.Project, &p.Subject, &p.Verb, &p.Target, &p.Payload,
			&p.State, &p.Reason, &p.Error, &p.ResolvedBy, &p.CreatedAt,
			&p.TabID, &p.WorkspaceID, &p.AllProjects, &p.BaseUpdatedAt); err != nil {
			return nil, wrap(err)
		}
		out = append(out, p)
	}
	return out, wrap(rows.Err())
}

// GetParked reads one parked action. "It is not there" and "I could not look"
// are different answers: reporting the first for a locked or broken database
// tells the operator resolving an action that the action has gone, which is
// the one thing it certainly has not. readTask and readNote already tell them
// apart; this is the same shape.
func (s *Store) GetParked(project, id string) (*Parked, error) {
	var p Parked
	err := s.db.QueryRow(
		`SELECT id, project, subject, verb, target, payload, state, COALESCE(reason, ''),
		        COALESCE(error, ''), COALESCE(resolved_by, ''), created_at,
		        COALESCE(tab_id, ''), COALESCE(workspace_id, ''), COALESCE(all_projects, 0),
		        COALESCE(base_updated_at, 0)
		 FROM parked WHERE project = ? AND id = ?`, project, id).
		Scan(&p.ID, &p.Project, &p.Subject, &p.Verb, &p.Target, &p.Payload, &p.State, &p.Reason,
			&p.Error, &p.ResolvedBy, &p.CreatedAt, &p.TabID, &p.WorkspaceID, &p.AllProjects, &p.BaseUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, codes.Errorf(codes.NotFound, "no parked action %s", id)
	}
	if err != nil {
		return nil, wrap(err)
	}
	return &p, nil
}

// ResolveParked closes a parked action, recording the principal that decided
// it. Any principal may reach this verb (§3.7); by is what the row says about
// who did, which is why it is not optional. It is the whole Actor rather than
// its principal because §3.7 asks the event for a second fact — whether the
// operator's authority was exercised by someone else — and that is read off
// the actor.
func (s *Store) ResolveParked(project, id, state string, by tasks.Actor, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		"UPDATE parked SET state = ?, resolved_by = ?, resolved_at = ? WHERE project = ? AND id = ? AND state = 'parked'",
		state, string(by.Principal), now, project, id)
	if err != nil {
		return wrap(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return codes.Errorf(codes.Conflict, "parked action %s is not waiting", id)
	}
	// The state a parked row moves to IS the kind of the event that moved it,
	// and the actor is the principal that decided it — which the row itself
	// keeps only until the next write touches it (§5.5). Resolving a deferral
	// is the operator's authority (§3.7), so when the decider is not the
	// operator the event carries the same mark the five note verbs write, and
	// the trail of a resolve an agent performed after confirming reads the way
	// a promote or a drop it performed does.
	if err := appendEvent(tx, "parked_events", id, project, tasks.Event{
		Kind: state, Actor: by.Principal, At: now,
		Detail: tasks.MarkOperatorVerb(by, nil),
	}); err != nil {
		return wrap(err)
	}
	return wrap(tx.Commit())
}

// FailParked records that a verb the operator resolved did not run. The row
// stays decided — it is not put back to `parked`, because a dispatch that
// errored is not proof it had no effect, and because re-opening it reopens the
// window this ordering exists to close. The operator sees why and decides
// again, deliberately.
//
// by is the principal that resolved it, the same Actor the caller passed to
// ResolveParked a moment earlier. It is the whole Actor for the reason
// ResolveParked's is: a failed re-run is the tail of one operator verb, so its
// event carries the same §3.7 mark as the `resolved` event it follows, and a
// trail that marked the resolve but not the failure would say the authority
// was the caller's own for the half of the story that says the verb did not
// run.
func (s *Store) FailParked(project, id, message string, by tasks.Actor, now int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return wrap(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		"UPDATE parked SET state = 'failed', error = ?, resolved_at = ? WHERE project = ? AND id = ? AND state = 'resolved'",
		message, now, project, id)
	if err != nil {
		return wrap(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return codes.Errorf(codes.Conflict, "parked action %s is not one this call resolved", id)
	}
	if err := appendEvent(tx, "parked_events", id, project, tasks.Event{
		Kind: KindFailed, Actor: by.Principal, At: now,
		Detail: tasks.MarkOperatorVerb(by, map[string]any{"error": message}),
	}); err != nil {
		return wrap(err)
	}
	return wrap(tx.Commit())
}
