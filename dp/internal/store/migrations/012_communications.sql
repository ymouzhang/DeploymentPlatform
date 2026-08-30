CREATE TABLE communication_threads (
    id TEXT PRIMARY KEY,
    target_user_id TEXT NOT NULL,
    target_username TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('open', 'closed')),
    created_by TEXT NOT NULL,
    created_by_username TEXT NOT NULL,
    closed_by TEXT NOT NULL DEFAULT '',
    closed_by_username TEXT NOT NULL DEFAULT '',
    closed_at TEXT,
    reopen_count INTEGER NOT NULL DEFAULT 0,
    last_reopened_by TEXT NOT NULL DEFAULT '',
    last_reopened_by_username TEXT NOT NULL DEFAULT '',
    last_reopened_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_communication_threads_updated ON communication_threads(updated_at DESC, id DESC);
CREATE INDEX idx_communication_threads_target_updated ON communication_threads(target_user_id, updated_at DESC, id DESC);
CREATE INDEX idx_communication_threads_status_updated ON communication_threads(status, updated_at DESC, id DESC);

CREATE TABLE communication_messages (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL,
    type TEXT NOT NULL CHECK(type IN ('admin_message', 'user_receipt', 'system_closed', 'system_reopened')),
    sender_user_id TEXT NOT NULL,
    sender_username TEXT NOT NULL,
    sender_role TEXT NOT NULL CHECK(sender_role IN ('admin', 'user')),
    content TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY(thread_id) REFERENCES communication_threads(id) ON DELETE CASCADE
);

CREATE INDEX idx_communication_messages_thread_time ON communication_messages(thread_id, created_at, id);

CREATE TABLE communication_message_recipients (
    message_id TEXT NOT NULL,
    recipient_user_id TEXT NOT NULL,
    recipient_username TEXT NOT NULL,
    recipient_role TEXT NOT NULL CHECK(recipient_role IN ('admin', 'user')),
    read_at TEXT,
    PRIMARY KEY(message_id, recipient_user_id),
    FOREIGN KEY(message_id) REFERENCES communication_messages(id) ON DELETE CASCADE
);

CREATE INDEX idx_communication_recipients_unread ON communication_message_recipients(recipient_user_id, read_at, message_id);

CREATE TABLE communication_resource_refs (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL,
    resource_type TEXT NOT NULL CHECK(resource_type IN ('package', 'environment', 'service')),
    resource_id TEXT NOT NULL DEFAULT '',
    resource_key TEXT NOT NULL DEFAULT '',
    owner_id TEXT NOT NULL,
    owner_username TEXT NOT NULL,
    resource_label TEXT NOT NULL,
    service_type TEXT NOT NULL DEFAULT '',
    environment_name TEXT NOT NULL DEFAULT '',
    environment_ip TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY(thread_id) REFERENCES communication_threads(id) ON DELETE CASCADE,
    UNIQUE(thread_id, resource_type, resource_id, resource_key)
);

CREATE INDEX idx_communication_resources_thread ON communication_resource_refs(thread_id, created_at, id);
