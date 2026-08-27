CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('admin', 'user')),
    enabled INTEGER NOT NULL DEFAULT 1,
    is_initial_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO users (
    id, username, password_hash, role, enabled, is_initial_admin, created_at, updated_at
) VALUES (
    '00000000-0000-4000-8000-000000000001',
    '__dp_pending_admin__',
    '!',
    'admin',
    1,
    1,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

ALTER TABLE service_configs RENAME TO service_configs_old;
ALTER TABLE operations RENAME TO operations_old;
ALTER TABLE environments RENAME TO environments_old;
ALTER TABLE packages RENAME TO packages_old;

CREATE TABLE environments (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
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
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    arch TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    last_validation_at TEXT,
    FOREIGN KEY(owner_id) REFERENCES users(id),
    UNIQUE(owner_id, ip, service_type)
);

INSERT INTO environments (
    id, owner_id, name, ip, ssh_user, ssh_port, ssh_password_enc, install_dir,
    service_type, installed, installed_at, installed_package_sha256, health_port,
    host_key_fingerprint, created_at, updated_at, arch, note, last_validation_at
)
SELECT
    id, '00000000-0000-4000-8000-000000000001', name, ip, ssh_user, ssh_port,
    ssh_password_enc, install_dir, service_type, installed, installed_at,
    installed_package_sha256, health_port, host_key_fingerprint, created_at,
    updated_at, arch, note, last_validation_at
FROM environments_old;

CREATE TABLE packages (
    owner_id TEXT NOT NULL,
    service_type TEXT NOT NULL,
    original_filename TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    config_port INTEGER NOT NULL,
    uploaded_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(owner_id, service_type),
    FOREIGN KEY(owner_id) REFERENCES users(id)
);

INSERT INTO packages (
    owner_id, service_type, original_filename, storage_path, sha256, size_bytes,
    config_port, uploaded_at, updated_at, note
)
SELECT
    '00000000-0000-4000-8000-000000000001', service_type, original_filename,
    storage_path, sha256, size_bytes, config_port, uploaded_at, updated_at, note
FROM packages_old;

CREATE TABLE service_configs (
    environment_id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    format TEXT NOT NULL,
    path TEXT NOT NULL,
    port INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(environment_id) REFERENCES environments(id) ON DELETE CASCADE
);

INSERT INTO service_configs (environment_id, content, format, path, port, updated_at)
SELECT environment_id, content, format, path, port, updated_at
FROM service_configs_old;

CREATE TABLE operations (
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

INSERT INTO operations (
    id, environment_id, action, status, stage, exit_code, error_code,
    error_message, log_path, created_at, started_at, finished_at
)
SELECT
    id, environment_id, action, status, stage, exit_code, error_code,
    error_message, log_path, created_at, started_at, finished_at
FROM operations_old;

DROP TABLE service_configs_old;
DROP TABLE operations_old;
DROP TABLE environments_old;
DROP TABLE packages_old;

CREATE INDEX idx_operations_environment_created
    ON operations(environment_id, created_at DESC);
