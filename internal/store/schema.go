package store

import "database/sql"

// SchemaVersion is the migration the daemon in this binary knows. A store
// stamped higher than this was written by a newer daemon: refuse, never
// downgrade (§5.2).
const SchemaVersion = 5

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
}
