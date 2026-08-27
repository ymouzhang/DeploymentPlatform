ALTER TABLE operations RENAME TO operations_old;

CREATE TABLE operations (
    id TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL,
    actor_user_id TEXT NOT NULL DEFAULT '',
    actor_username TEXT NOT NULL DEFAULT '',
    owner_id TEXT NOT NULL DEFAULT '',
    owner_username TEXT NOT NULL DEFAULT '',
    environment_name TEXT NOT NULL DEFAULT '',
    environment_ip TEXT NOT NULL DEFAULT '',
    service_type TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    status TEXT NOT NULL,
    stage TEXT NOT NULL DEFAULT '',
    exit_code INTEGER,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    log_path TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT
);

INSERT INTO operations (
    id, environment_id, owner_id, owner_username, environment_name, environment_ip,
    service_type, action, status, stage, exit_code, error_code, error_message,
    log_path, created_at, started_at, finished_at
)
SELECT o.id, o.environment_id, COALESCE(e.owner_id, ''), COALESCE(u.username, ''),
    COALESCE(e.name, ''), COALESCE(e.ip, ''), COALESCE(e.service_type, ''),
    o.action, o.status, o.stage, o.exit_code, o.error_code, o.error_message,
    o.log_path, o.created_at, o.started_at, o.finished_at
FROM operations_old o
LEFT JOIN environments e ON e.id = o.environment_id
LEFT JOIN users u ON u.id = e.owner_id;

DROP TABLE operations_old;

CREATE INDEX idx_operations_environment_created ON operations(environment_id, created_at DESC);
CREATE INDEX idx_operations_created ON operations(created_at DESC, id DESC);
CREATE INDEX idx_operations_owner_created ON operations(owner_id, created_at DESC);
CREATE INDEX idx_operations_actor_created ON operations(actor_user_id, created_at DESC);
CREATE INDEX idx_operations_status_created ON operations(status, created_at DESC);

CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    risk_level TEXT NOT NULL CHECK(risk_level IN ('normal', 'high')),
    category TEXT NOT NULL CHECK(category IN ('security', 'account', 'resource', 'operation', 'system')),
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    target_label TEXT NOT NULL DEFAULT '',
    owner_id TEXT,
    owner_username TEXT NOT NULL DEFAULT '',
    operation_id TEXT,
    link TEXT NOT NULL DEFAULT '/dashboard',
    read_at TEXT,
    read_by TEXT,
    resolved_at TEXT,
    resolved_by TEXT
);

CREATE INDEX idx_notifications_created ON notifications(created_at DESC, id DESC);
CREATE INDEX idx_notifications_unread ON notifications(read_at, created_at DESC);
CREATE INDEX idx_notifications_unresolved ON notifications(resolved_at, created_at DESC);

ALTER TABLE users ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
