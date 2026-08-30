ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0;

ALTER TABLE sessions ADD COLUMN id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN source_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN last_seen_at TEXT NOT NULL DEFAULT '';

UPDATE sessions
SET id = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
    substr(lower(hex(randomblob(2))), 2) || '-' ||
    substr('89ab', (random() & 3) + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' ||
    lower(hex(randomblob(6))),
    last_seen_at = created_at;

CREATE UNIQUE INDEX idx_sessions_id ON sessions(id);

CREATE TABLE login_throttles (
    scope_key TEXT PRIMARY KEY,
    failure_count INTEGER NOT NULL,
    window_started_at TEXT NOT NULL,
    blocked_until TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_login_throttles_updated ON login_throttles(updated_at);
