CREATE TABLE resource_tags (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    group_name TEXT NOT NULL COLLATE NOCASE,
    value TEXT NOT NULL COLLATE NOCASE,
    deleted_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(owner_id) REFERENCES users(id)
);

CREATE TABLE environment_tags (
    environment_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    PRIMARY KEY(environment_id, tag_id),
    FOREIGN KEY(environment_id) REFERENCES environments(id) ON DELETE CASCADE,
    FOREIGN KEY(tag_id) REFERENCES resource_tags(id) ON DELETE CASCADE
);

CREATE TRIGGER environment_tags_same_owner_insert
BEFORE INSERT ON environment_tags
WHEN (SELECT owner_id FROM environments WHERE id = NEW.environment_id) !=
     (SELECT owner_id FROM resource_tags WHERE id = NEW.tag_id)
BEGIN
    SELECT RAISE(ABORT, 'environment and tag owners differ');
END;

CREATE TABLE operation_tags (
    operation_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    group_name TEXT NOT NULL COLLATE NOCASE,
    value TEXT NOT NULL COLLATE NOCASE,
    PRIMARY KEY(operation_id, tag_id),
    FOREIGN KEY(operation_id) REFERENCES operations(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_resource_tags_owner_name ON resource_tags(owner_id, group_name, value) WHERE deleted_at IS NULL;
CREATE INDEX idx_environment_tags_tag ON environment_tags(tag_id, environment_id);
CREATE INDEX idx_operation_tags_value ON operation_tags(group_name, value, operation_id);
CREATE INDEX idx_operation_tags_tag ON operation_tags(tag_id, operation_id);
