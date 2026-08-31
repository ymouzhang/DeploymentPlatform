CREATE TABLE models (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    marker_owner_id TEXT NOT NULL,
    environment_id TEXT NOT NULL,
    environment_name TEXT NOT NULL,
    environment_ip TEXT NOT NULL,
    name TEXT NOT NULL,
    source TEXT NOT NULL CHECK(source IN ('offline', 'modelscope', 'huggingface')),
    target_dir TEXT NOT NULL,
    original_filename TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    expanded_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_count INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    error_message TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_by_username TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    ready_at TEXT,
    deleted_at TEXT
);

CREATE UNIQUE INDEX idx_models_owner_target_active
    ON models(owner_id, environment_ip, target_dir)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_models_owner_created ON models(owner_id, created_at DESC);
CREATE INDEX idx_models_environment_active ON models(environment_id) WHERE deleted_at IS NULL;

CREATE TABLE model_uploads (
    id TEXT PRIMARY KEY,
    model_id TEXT NOT NULL UNIQUE,
    owner_id TEXT NOT NULL,
    remote_path TEXT NOT NULL,
    offset INTEGER NOT NULL DEFAULT 0,
    total_bytes INTEGER NOT NULL,
    status TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(model_id) REFERENCES models(id) ON DELETE CASCADE
);
CREATE INDEX idx_model_uploads_expiry ON model_uploads(status, expires_at);

CREATE TABLE model_tasks (
    id TEXT PRIMARY KEY,
    model_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    actor_user_id TEXT NOT NULL DEFAULT '',
    actor_username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL CHECK(action IN ('deploy', 'delete')),
    status TEXT NOT NULL,
    stage TEXT NOT NULL DEFAULT '',
    progress INTEGER NOT NULL DEFAULT 0,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    log_path TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    FOREIGN KEY(model_id) REFERENCES models(id)
);
CREATE INDEX idx_model_tasks_model_created ON model_tasks(model_id, created_at DESC);
