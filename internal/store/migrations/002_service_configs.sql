CREATE TABLE IF NOT EXISTS service_configs (
    environment_id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    format TEXT NOT NULL,
    path TEXT NOT NULL,
    port INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY(environment_id) REFERENCES environments(id) ON DELETE CASCADE
);
