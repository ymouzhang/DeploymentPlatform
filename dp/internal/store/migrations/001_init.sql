CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS environments (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    ip TEXT NOT NULL,
    ssh_user TEXT NOT NULL,
    ssh_port INTEGER NOT NULL,
    ssh_password_enc TEXT NOT NULL,
    install_dir TEXT NOT NULL,
    service_type TEXT NOT NULL,
    installed INTEGER NOT NULL DEFAULT 0,
    installed_at TEXT,
    installed_package_sha256 TEXT NOT NULL DEFAULT '',
    health_port INTEGER,
    host_key_fingerprint TEXT NOT NULL DEFAULT '',
    last_validation_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(ip, service_type)
);

CREATE TABLE IF NOT EXISTS packages (
    service_type TEXT PRIMARY KEY,
    original_filename TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    config_port INTEGER NOT NULL,
    uploaded_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS operations (
    id TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL,
    action TEXT NOT NULL,
    status TEXT NOT NULL,
    stage TEXT NOT NULL DEFAULT '',
    exit_code INTEGER,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    log_path TEXT NOT NULL,
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    FOREIGN KEY(environment_id) REFERENCES environments(id)
);

CREATE INDEX IF NOT EXISTS idx_operations_environment_created
    ON operations(environment_id, created_at DESC);
