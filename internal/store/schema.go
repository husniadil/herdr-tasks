package store

import "database/sql"

// SchemaVersion is the migration the daemon in this binary knows. A store
// stamped higher than this was written by a newer daemon: refuse, never
// downgrade (§5.2).
const SchemaVersion = 11

// migration is one numbered step. Most are SQL; one has to be Go, because
// re-encoding every stored id is not something SQL can do.
type migration struct {
	SQL string
	Fn  func(*sql.Tx) error
}

// migrations are numbered and append-only (§5.2). Index i holds migration
// i+1; each runs in its own transaction at daemon start. Never edit a shipped
// entry — append a new one.
var migrations = []migration{{SQL:
// 1 — tasks, notes, their append-only event tables, the per-project seq
// counter, and the parked queue of the policy gate.
`
CREATE TABLE meta (
  schema_version INTEGER NOT NULL,
  created_at     INTEGER NOT NULL
);

CREATE TABLE seqs (
  project TEXT    NOT NULL,
  entity  TEXT    NOT NULL,
  next    INTEGER NOT NULL,
  PRIMARY KEY (project, entity)
);

CREATE TABLE tasks (
  id                  TEXT    PRIMARY KEY,
  seq                 INTEGER NOT NULL,
  project             TEXT    NOT NULL,
  title               TEXT    NOT NULL,
  description         TEXT    NOT NULL DEFAULT '',
  status              TEXT    NOT NULL,
  priority            INTEGER NOT NULL DEFAULT 0,
  validation          TEXT,
  discovered_from     TEXT,
  created_by          TEXT    NOT NULL,
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL,
  claimed_by          TEXT,
  claimed_by_name     TEXT,
  claimed_by_harness  TEXT,
  claimed_by_session  TEXT,
  claimed_at          INTEGER,
  lease_until         INTEGER,
  ever_claimed        INTEGER NOT NULL DEFAULT 0,
  release_note        TEXT,
  released_at         INTEGER,
  report              TEXT,
  evidence            TEXT,
  submitted_by        TEXT,
  submitted_by_harness TEXT,
  submitted_at        INTEGER,
  feedback            TEXT,
  reviewed_by         TEXT,
  completed_at        INTEGER,
  cancelled_at        INTEGER,
  archived_at         INTEGER,
  pane_id             TEXT,
  tab_id              TEXT,
  workspace_id        TEXT
);
CREATE UNIQUE INDEX tasks_project_seq ON tasks (project, seq);
CREATE INDEX tasks_project_status ON tasks (project, status);

CREATE TABLE task_deps (
  task_id       TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  depends_on_id TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, depends_on_id)
);
CREATE INDEX task_deps_depends_on ON task_deps (depends_on_id);

CREATE TABLE tasks_events (
  id        TEXT    PRIMARY KEY,
  entity_id TEXT    NOT NULL,
  project   TEXT    NOT NULL,
  at        INTEGER NOT NULL,
  actor     TEXT    NOT NULL,
  kind      TEXT    NOT NULL,
  detail    TEXT
);
CREATE INDEX tasks_events_entity ON tasks_events (entity_id);

CREATE TABLE notes (
  id             TEXT    PRIMARY KEY,
  seq            INTEGER NOT NULL,
  project        TEXT    NOT NULL,
  body           TEXT    NOT NULL,
  status         TEXT    NOT NULL,
  author         TEXT    NOT NULL,
  author_name    TEXT,
  author_harness TEXT,
  verdict        TEXT,
  reason         TEXT,
  question       TEXT,
  task_id        TEXT,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  pane_id        TEXT
);
CREATE UNIQUE INDEX notes_project_seq ON notes (project, seq);
CREATE INDEX notes_project_status ON notes (project, status);

CREATE TABLE notes_events (
  id        TEXT    PRIMARY KEY,
  entity_id TEXT    NOT NULL,
  project   TEXT    NOT NULL,
  at        INTEGER NOT NULL,
  actor     TEXT    NOT NULL,
  kind      TEXT    NOT NULL,
  detail    TEXT
);
CREATE INDEX notes_events_entity ON notes_events (entity_id);

CREATE TABLE parked (
  id          TEXT    PRIMARY KEY,
  project     TEXT    NOT NULL,
  subject     TEXT    NOT NULL,
  verb        TEXT    NOT NULL,
  target      TEXT    NOT NULL,
  payload     TEXT    NOT NULL DEFAULT '{}',
  state       TEXT    NOT NULL DEFAULT 'parked',
  reason      TEXT,
  created_at  INTEGER NOT NULL,
  resolved_at INTEGER
);
CREATE INDEX parked_project_state ON parked (project, state);
`},
	// 2 — why a parked action the operator resolved did not happen. Before
	// this the verb ran before the row was marked, so a failure left the row
	// waiting and re-runnable; now the row is marked first and moved to
	// `failed` with the reason when the verb refuses.
	{SQL: `ALTER TABLE parked ADD COLUMN error TEXT;`},
	// 3 — re-encode every stored id. §5.4 names ULIDs, and this store minted
	// something that was not one: the 128 bits were rendered LEFT-aligned in
	// the 26 characters, two bits out, so a decoder that did not know about
	// this implementation read the timestamp as a date two centuries away.
	// Ordering was never wrong — a constant shift preserves it — but a
	// half-migrated store WOULD be, because for the same instant an old id
	// sorts after a new one. So every id moves in one transaction, or none
	// does.
	{Fn: reencodeIDs},
	// 4 — which acceptance criterion a piece of evidence proves (§16.1). A NEW
	// column, never a change to `evidence`: that one is a shipped --json field
	// and a row written before this migration must read back exactly as it
	// did, which is what TestEvidenceForFlatRows holds.
	{SQL: `ALTER TABLE tasks ADD COLUMN evidence_for TEXT;`},
	// 5 — the agent session that submitted the work, which §6.6 recuses on
	// from contract revision 0.6.0. A NEW column beside submitted_by_harness,
	// never a change to it: a row written before this migration has no
	// session, reads back NULL, and CheckRecusal treats an absent producer
	// session as no match rather than guessing one.
	{SQL: `ALTER TABLE tasks ADD COLUMN submitted_by_session TEXT;`},
	// 6 — the board a promoted note's task landed on, so a promotion that
	// crossed projects can be followed. A NEW column beside task_id, never a
	// change to it: a row written before this migration was promoted within
	// its own project, reads back NULL, and an empty task_project means
	// exactly that.
	{SQL: `ALTER TABLE notes ADD COLUMN task_project TEXT;`},
	// 7 — whether a note reached its task by being folded into it rather than
	// by being promoted into it. A NEW column beside task_id, never a change
	// to it: a row written before this migration reached its task by
	// promotion, reads back NULL, and a false `folded` means exactly that.
	{SQL: `ALTER TABLE notes ADD COLUMN folded INTEGER;`},
	// 8 — the principal that resolved or rejected a parked action. A NEW
	// column beside resolved_at, never a change to it: a row written before
	// this migration was resolved when only the operator could reach the verb
	// (§9.3), reads back NULL, and an empty resolved_by means exactly that.
	// It exists because §3.7 made the operator's authority advice an agent
	// confirms, and §9.3 re-runs the verb under the ORIGINAL subject — so
	// without this column the trail names the deferred agent and no one else.
	{SQL: `ALTER TABLE parked ADD COLUMN resolved_by TEXT;`},
	// 9 — when a submitted report was last corrected, and how many times. NEW
	// columns beside submitted_at, never a change to it: a row written before
	// this migration was never amended, reads back NULL, and a zero
	// amend_count means exactly that. They exist because `task amend` lets the
	// holder correct a report while it is in review, and a reviewer reading
	// the row afterwards has to be able to see that it happened without
	// relying on the worker having said so in a message that lives in another
	// store.
	{SQL: `ALTER TABLE tasks ADD COLUMN amended_at INTEGER;
ALTER TABLE tasks ADD COLUMN amend_count INTEGER;`},
	// 10 — the tab, the workspace and the project scope a deferred call was
	// made with. NEW columns beside payload, never a change to it: §9.3
	// re-runs the verb as the ORIGINAL call, and the payload only ever held
	// the verb's own arguments, so everything the door derived — where the
	// pane sat, and whether the caller asked for the whole fleet — was gone
	// by the time the operator resolved it. A row written before this
	// migration reads back NULL and 0, which is what a call with no tab, no
	// workspace and no --all-projects looks like.
	{SQL: `ALTER TABLE parked ADD COLUMN tab_id TEXT;
ALTER TABLE parked ADD COLUMN workspace_id TEXT;
ALTER TABLE parked ADD COLUMN all_projects INTEGER;`},
	// 11 — the append-only sibling of `parked`, which §5.5 gives every entity
	// with a table and this one never had. The `parked` row is mutated in
	// place, so what the gate deferred and what the operator then decided
	// about it survived only as the row's current state: resolving one erased
	// when and against whom it had been parked, and no §8.1 consumer — a hook
	// or an `events --follow` stream — ever learned a gate decision had
	// happened at all. The shape is the other two tables', column for column,
	// so one reader serves all three.
	{SQL: `
CREATE TABLE parked_events (
  id        TEXT    PRIMARY KEY,
  entity_id TEXT    NOT NULL,
  project   TEXT    NOT NULL,
  at        INTEGER NOT NULL,
  actor     TEXT    NOT NULL,
  kind      TEXT    NOT NULL,
  detail    TEXT
);
CREATE INDEX parked_events_entity ON parked_events (entity_id);
`},
}
