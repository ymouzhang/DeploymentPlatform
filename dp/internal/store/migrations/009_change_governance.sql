ALTER TABLE operations ADD COLUMN request_id TEXT NOT NULL DEFAULT '';

ALTER TABLE notifications ADD COLUMN dedupe_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_notifications_open_dedupe
    ON notifications(dedupe_key) WHERE dedupe_key <> '' AND resolved_at IS NULL;

ALTER TABLE packages ADD COLUMN current_version_id TEXT NOT NULL DEFAULT '';

CREATE TABLE package_versions (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    service_type TEXT NOT NULL,
    original_filename TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    config_port INTEGER NOT NULL,
    config_format TEXT NOT NULL DEFAULT '',
    config_path TEXT NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    uploaded_by TEXT NOT NULL DEFAULT '',
    uploaded_by_username TEXT NOT NULL DEFAULT '',
    uploaded_at TEXT NOT NULL,
    UNIQUE(owner_id, service_type, sha256),
    FOREIGN KEY(owner_id) REFERENCES users(id)
);

INSERT INTO package_versions (
    id, owner_id, service_type, original_filename, storage_path, sha256, size_bytes,
    config_port, note, uploaded_at
)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
       substr(lower(hex(randomblob(2))), 2) || '-a' || substr(lower(hex(randomblob(2))), 2) || '-' ||
       lower(hex(randomblob(6))),
       owner_id, service_type, original_filename, storage_path, sha256, size_bytes,
       config_port, note, uploaded_at
FROM packages;

UPDATE packages SET current_version_id = (
    SELECT id FROM package_versions v
    WHERE v.owner_id = packages.owner_id AND v.service_type = packages.service_type
);

CREATE INDEX idx_package_versions_owner_type_time
    ON package_versions(owner_id, service_type, uploaded_at DESC, id DESC);

ALTER TABLE service_configs ADD COLUMN current_revision_id TEXT NOT NULL DEFAULT '';

CREATE TABLE service_config_revisions (
    id TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL,
    content TEXT NOT NULL,
    format TEXT NOT NULL CHECK(format IN ('json', 'yaml')),
    path TEXT NOT NULL,
    port INTEGER NOT NULL,
    source TEXT NOT NULL CHECK(source IN ('manual', 'rollback')),
    restored_from_id TEXT,
    created_by TEXT NOT NULL DEFAULT '',
    created_by_username TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY(environment_id) REFERENCES environments(id) ON DELETE CASCADE
);

INSERT INTO service_config_revisions (
    id, environment_id, content, format, path, port, source, created_at
)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
       substr(lower(hex(randomblob(2))), 2) || '-a' || substr(lower(hex(randomblob(2))), 2) || '-' ||
       lower(hex(randomblob(6))),
       environment_id, content, format, path, port, 'manual', updated_at
FROM service_configs;

UPDATE service_configs SET current_revision_id = (
    SELECT id FROM service_config_revisions r WHERE r.environment_id = service_configs.environment_id
);

CREATE INDEX idx_config_revisions_environment_time
    ON service_config_revisions(environment_id, created_at DESC, id DESC);
