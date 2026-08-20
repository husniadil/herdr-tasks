package store

import (
	"github.com/husniadil/herdr-tasks/internal/codes"
	"github.com/husniadil/herdr-tasks/internal/ids"
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
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	// Error is why the verb failed when the operator resolved it. A parked
	// action that was decided and did not happen is not a resolved one, and
	// the operator needs to see which.
	Error      string `json:"error,omitempty"`
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
	_, err := s.db.Exec(
		`INSERT INTO parked (id, project, subject, verb, target, payload, state, reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'parked', ?, ?)`,
		id, p.Project, p.Subject, p.Verb, p.Target, p.Payload, nullIfEmpty(p.Reason), now)
	return id, wrap(err)
}

// ListParked returns the actions in a project that still want the operator's
// attention: the ones still waiting, and the ones they resolved whose verb
// then failed. A failed action is not finished business — hiding it would make
// a verb that did not happen look like one that did.
func (s *Store) ListParked(project string) ([]Parked, error) {
	rows, err := s.db.Query(
		`SELECT id, project, subject, verb, target, payload, state, COALESCE(reason, ''),
		        COALESCE(error, ''), created_at
		 FROM parked WHERE project = ? AND state IN ('parked', 'failed') ORDER BY id ASC`, project)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []Parked{}
	for rows.Next() {
		var p Parked
		if err := rows.Scan(&p.ID, &p.Project, &p.Subject, &p.Verb, &p.Target, &p.Payload,
			&p.State, &p.Reason, &p.Error, &p.CreatedAt); err != nil {
			return nil, wrap(err)
		}
		out = append(out, p)
	}
	return out, wrap(rows.Err())
}

// GetParked reads one parked action.
func (s *Store) GetParked(project, id string) (*Parked, error) {
	var p Parked
	err := s.db.QueryRow(
		`SELECT id, project, subject, verb, target, payload, state, COALESCE(reason, ''),
		        COALESCE(error, ''), created_at
		 FROM parked WHERE project = ? AND id = ?`, project, id).
		Scan(&p.ID, &p.Project, &p.Subject, &p.Verb, &p.Target, &p.Payload, &p.State, &p.Reason,
			&p.Error, &p.CreatedAt)
	if err != nil {
		return nil, codes.Errorf(codes.NotFound, "no parked action %s", id)
	}
	return &p, nil
}

// ResolveParked closes a parked action. Only the operator calls this; the door
// enforces that, because §9.3 puts the rule on the principal, not the row.
func (s *Store) ResolveParked(project, id, state string, now int64) error {
	res, err := s.db.Exec(
		"UPDATE parked SET state = ?, resolved_at = ? WHERE project = ? AND id = ? AND state = 'parked'",
		state, now, project, id)
	if err != nil {
		return wrap(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return codes.Errorf(codes.Conflict, "parked action %s is not waiting", id)
	}
	return nil
}

// FailParked records that a verb the operator resolved did not run. The row
// stays decided — it is not put back to `parked`, because a dispatch that
// errored is not proof it had no effect, and because re-opening it reopens the
// window this ordering exists to close. The operator sees why and decides
// again, deliberately.
func (s *Store) FailParked(project, id, message string, now int64) error {
	res, err := s.db.Exec(
		"UPDATE parked SET state = 'failed', error = ?, resolved_at = ? WHERE project = ? AND id = ? AND state = 'resolved'",
		message, now, project, id)
	if err != nil {
		return wrap(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return codes.Errorf(codes.Conflict, "parked action %s is not one this call resolved", id)
	}
	return nil
}
