package store

// SchemaVersion is the migration the daemon in this binary knows. A store
// stamped higher than this was written by a newer daemon: refuse, never
// downgrade (§5.2).
const SchemaVersion = 1

// migrations are numbered and append-only (§5.2). Index i holds migration
// i+1; each runs in its own transaction at daemon start. Never edit a shipped
// entry — append a new one.
var migrations = []string{
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
`,
}
