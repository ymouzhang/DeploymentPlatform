CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    occurred_at TEXT NOT NULL,
    category TEXT NOT NULL,
    action TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK(outcome IN ('success', 'failure', 'denied')),
    risk_level TEXT NOT NULL CHECK(risk_level IN ('normal', 'high')),
    actor_user_id TEXT,
    actor_username TEXT NOT NULL DEFAULT '',
    actor_role TEXT NOT NULL DEFAULT '',
    owner_id TEXT,
    owner_username TEXT NOT NULL DEFAULT '',
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    target_label TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    operation_id TEXT,
    source_ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    changes_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_audit_events_time ON audit_events(occurred_at DESC, id DESC);
CREATE INDEX idx_audit_events_actor_time ON audit_events(actor_user_id, occurred_at DESC);
CREATE INDEX idx_audit_events_owner_time ON audit_events(owner_id, occurred_at DESC);
CREATE INDEX idx_audit_events_action_time ON audit_events(action, occurred_at DESC);
CREATE INDEX idx_audit_events_outcome_time ON audit_events(outcome, occurred_at DESC);
CREATE INDEX idx_audit_events_operation ON audit_events(operation_id, occurred_at DESC);
