CREATE TABLE users (
    id UUID PRIMARY KEY,
    username VARCHAR(32) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    must_change_password BOOLEAN NOT NULL DEFAULT TRUE,
    is_initial_admin BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE roles (
    id UUID PRIMARY KEY,
    key VARCHAR(63) NOT NULL UNIQUE,
    name VARCHAR(64) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    system BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT roles_key_format CHECK (key ~ '^[a-z][a-z0-9_-]{1,62}$')
);

CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key VARCHAR(127) NOT NULL UNIQUE,
    resource VARCHAR(63) NOT NULL,
    action VARCHAR(63) NOT NULL,
    description VARCHAR(255) NOT NULL,
    scoped BOOLEAN NOT NULL,
    CONSTRAINT permissions_key_format CHECK (key ~ '^[a-z][a-z0-9_.]{2,126}$'),
    UNIQUE (resource, action)
);

CREATE TABLE role_permissions (
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE RESTRICT,
    scope VARCHAR(8) NOT NULL,
    PRIMARY KEY (role_id, permission_id),
    CONSTRAINT role_permissions_scope CHECK (scope IN ('own', 'all'))
);

CREATE TABLE user_roles (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    assigned_by UUID REFERENCES users(id) ON DELETE SET NULL,
    assigned_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX idx_user_roles_role ON user_roles(role_id, user_id);

CREATE TABLE sessions (
    token_hash CHAR(64) PRIMARY KEY,
    id UUID NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_ip TEXT NOT NULL DEFAULT '',
    user_agent VARCHAR(512) NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE login_throttles (
    scope_key TEXT PRIMARY KEY,
    failure_count INTEGER NOT NULL CHECK (failure_count >= 0),
    window_started_at TIMESTAMPTZ NOT NULL,
    blocked_until TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_login_throttles_updated ON login_throttles(updated_at);

CREATE TABLE environments (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    ip INET NOT NULL,
    ssh_user TEXT NOT NULL,
    ssh_port INTEGER NOT NULL CHECK (ssh_port BETWEEN 1 AND 65535),
    ssh_password_enc TEXT NOT NULL,
    install_dir TEXT NOT NULL,
    service_type VARCHAR(63) NOT NULL,
    installed BOOLEAN NOT NULL DEFAULT FALSE,
    installed_at TIMESTAMPTZ,
    installed_package_sha256 CHAR(64),
    health_port INTEGER CHECK (health_port BETWEEN 1 AND 65535),
    host_key_fingerprint TEXT NOT NULL DEFAULT '',
    arch TEXT NOT NULL DEFAULT '',
    note VARCHAR(200) NOT NULL DEFAULT '',
    last_validation_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(owner_id, ip, service_type)
);
CREATE INDEX idx_environments_owner ON environments(owner_id, created_at DESC);

CREATE TABLE packages (
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    service_type VARCHAR(63) NOT NULL,
    current_version_id UUID,
    original_filename TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    config_port INTEGER NOT NULL CHECK (config_port BETWEEN 1 AND 65535),
    note VARCHAR(200) NOT NULL DEFAULT '',
    uploaded_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(owner_id, service_type)
);

CREATE TABLE package_versions (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    service_type VARCHAR(63) NOT NULL,
    original_filename TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    sha256 CHAR(64) NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    config_port INTEGER NOT NULL CHECK (config_port BETWEEN 1 AND 65535),
    config_format VARCHAR(8) NOT NULL CHECK (config_format IN ('json', 'yaml')),
    config_path TEXT NOT NULL,
    config_content BYTEA NOT NULL,
    note VARCHAR(200) NOT NULL DEFAULT '',
    uploaded_by UUID REFERENCES users(id) ON DELETE SET NULL,
    uploaded_by_username VARCHAR(32) NOT NULL DEFAULT '',
    uploaded_at TIMESTAMPTZ NOT NULL,
    UNIQUE(owner_id, service_type, sha256)
);
CREATE INDEX idx_package_versions_owner_type_time
    ON package_versions(owner_id, service_type, uploaded_at DESC, id DESC);

CREATE TABLE service_configs (
    environment_id UUID PRIMARY KEY REFERENCES environments(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    format VARCHAR(8) NOT NULL CHECK(format IN ('json', 'yaml')),
    path TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    current_revision_id UUID,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE service_config_revisions (
    id UUID PRIMARY KEY,
    environment_id UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    format VARCHAR(8) NOT NULL CHECK(format IN ('json', 'yaml')),
    path TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    source VARCHAR(16) NOT NULL CHECK(source IN ('manual', 'rollback')),
    restored_from_id UUID,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_by_username VARCHAR(32) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_config_revisions_environment_time
    ON service_config_revisions(environment_id, created_at DESC, id DESC);

CREATE TABLE operations (
    id UUID PRIMARY KEY,
    environment_id UUID NOT NULL,
    request_id UUID,
    actor_user_id UUID,
    actor_username VARCHAR(32) NOT NULL DEFAULT '',
    owner_id UUID,
    owner_username VARCHAR(32) NOT NULL DEFAULT '',
    environment_name TEXT NOT NULL DEFAULT '',
    environment_ip INET,
    service_type VARCHAR(63) NOT NULL DEFAULT '',
    action VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL,
    stage TEXT NOT NULL DEFAULT '',
    exit_code INTEGER,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    log_path TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_operations_environment_created ON operations(environment_id, created_at DESC);
CREATE INDEX idx_operations_created ON operations(created_at DESC, id DESC);
CREATE INDEX idx_operations_owner_created ON operations(owner_id, created_at DESC);
CREATE INDEX idx_operations_actor_created ON operations(actor_user_id, created_at DESC);
CREATE INDEX idx_operations_status_created ON operations(status, created_at DESC);

CREATE TABLE audit_events (
    id UUID PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    category TEXT NOT NULL,
    action TEXT NOT NULL,
    outcome VARCHAR(16) NOT NULL CHECK(outcome IN ('success', 'failure', 'denied')),
    risk_level VARCHAR(16) NOT NULL CHECK(risk_level IN ('normal', 'high')),
    actor_user_id UUID,
    actor_username VARCHAR(32) NOT NULL DEFAULT '',
    actor_role_keys JSONB NOT NULL DEFAULT '[]'::jsonb,
    owner_id UUID,
    owner_username VARCHAR(32) NOT NULL DEFAULT '',
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    target_label TEXT NOT NULL DEFAULT '',
    request_id UUID,
    operation_id UUID,
    source_ip INET,
    user_agent VARCHAR(512) NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    changes JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_audit_events_time ON audit_events(occurred_at DESC, id DESC);
CREATE INDEX idx_audit_events_actor_time ON audit_events(actor_user_id, occurred_at DESC);
CREATE INDEX idx_audit_events_owner_time ON audit_events(owner_id, occurred_at DESC);
CREATE INDEX idx_audit_events_action_time ON audit_events(action, occurred_at DESC);
CREATE INDEX idx_audit_events_outcome_time ON audit_events(outcome, occurred_at DESC);
CREATE INDEX idx_audit_events_operation ON audit_events(operation_id, occurred_at DESC);

CREATE TABLE notifications (
    id UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL,
    risk_level VARCHAR(16) NOT NULL CHECK(risk_level IN ('normal', 'high')),
    category VARCHAR(16) NOT NULL CHECK(category IN ('security', 'account', 'resource', 'operation', 'system')),
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    target_label TEXT NOT NULL DEFAULT '',
    owner_id UUID,
    owner_username VARCHAR(32) NOT NULL DEFAULT '',
    operation_id UUID,
    link TEXT NOT NULL DEFAULT '/dashboard',
    read_at TIMESTAMPTZ,
    read_by UUID,
    resolved_at TIMESTAMPTZ,
    resolved_by UUID,
    dedupe_key TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_notifications_created ON notifications(created_at DESC, id DESC);
CREATE INDEX idx_notifications_unread ON notifications(read_at, created_at DESC);
CREATE INDEX idx_notifications_unresolved ON notifications(resolved_at, created_at DESC);
CREATE UNIQUE INDEX idx_notifications_open_dedupe
    ON notifications(dedupe_key) WHERE dedupe_key <> '' AND resolved_at IS NULL;

CREATE TABLE resource_tags (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    group_name TEXT NOT NULL,
    value TEXT NOT NULL,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_resource_tags_owner_name
    ON resource_tags(owner_id, lower(group_name), lower(value)) WHERE deleted_at IS NULL;

CREATE TABLE environment_tags (
    environment_id UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES resource_tags(id) ON DELETE CASCADE,
    PRIMARY KEY(environment_id, tag_id)
);
CREATE INDEX idx_environment_tags_tag ON environment_tags(tag_id, environment_id);

CREATE FUNCTION check_environment_tag_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (SELECT owner_id FROM environments WHERE id = NEW.environment_id) IS DISTINCT FROM
       (SELECT owner_id FROM resource_tags WHERE id = NEW.tag_id) THEN
        RAISE EXCEPTION 'environment and tag owners differ' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER environment_tags_same_owner
    BEFORE INSERT OR UPDATE ON environment_tags
    FOR EACH ROW EXECUTE FUNCTION check_environment_tag_owner();

CREATE TABLE operation_tags (
    operation_id UUID NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL,
    group_name TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY(operation_id, tag_id)
);
CREATE INDEX idx_operation_tags_value ON operation_tags(lower(group_name), lower(value), operation_id);
CREATE INDEX idx_operation_tags_tag ON operation_tags(tag_id, operation_id);

CREATE TABLE communication_threads (
    id UUID PRIMARY KEY,
    target_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    target_username VARCHAR(32) NOT NULL,
    title VARCHAR(100) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK(status IN ('open', 'closed')),
    created_by UUID NOT NULL,
    created_by_username VARCHAR(32) NOT NULL,
    closed_by UUID,
    closed_by_username VARCHAR(32) NOT NULL DEFAULT '',
    closed_at TIMESTAMPTZ,
    reopen_count INTEGER NOT NULL DEFAULT 0 CHECK (reopen_count >= 0),
    last_reopened_by UUID,
    last_reopened_by_username VARCHAR(32) NOT NULL DEFAULT '',
    last_reopened_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_communication_threads_updated ON communication_threads(updated_at DESC, id DESC);
CREATE INDEX idx_communication_threads_target_updated ON communication_threads(target_user_id, updated_at DESC, id DESC);
CREATE INDEX idx_communication_threads_status_updated ON communication_threads(status, updated_at DESC, id DESC);

CREATE TABLE communication_messages (
    id UUID PRIMARY KEY,
    thread_id UUID NOT NULL REFERENCES communication_threads(id) ON DELETE CASCADE,
    type VARCHAR(32) NOT NULL CHECK(type IN ('message', 'receipt', 'system_closed', 'system_reopened')),
    sender_user_id UUID NOT NULL,
    sender_username VARCHAR(32) NOT NULL,
    sender_role_keys JSONB NOT NULL DEFAULT '[]'::jsonb,
    content VARCHAR(5000) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_communication_messages_thread_time ON communication_messages(thread_id, created_at, id);

CREATE TABLE communication_message_recipients (
    message_id UUID NOT NULL REFERENCES communication_messages(id) ON DELETE CASCADE,
    recipient_user_id UUID NOT NULL,
    recipient_username VARCHAR(32) NOT NULL,
    read_at TIMESTAMPTZ,
    PRIMARY KEY(message_id, recipient_user_id)
);
CREATE INDEX idx_communication_recipients_unread
    ON communication_message_recipients(recipient_user_id, read_at, message_id);

CREATE TABLE communication_resource_refs (
    id UUID PRIMARY KEY,
    thread_id UUID NOT NULL REFERENCES communication_threads(id) ON DELETE CASCADE,
    resource_type VARCHAR(16) NOT NULL CHECK(resource_type IN ('package', 'environment', 'service')),
    resource_id UUID,
    resource_key TEXT NOT NULL DEFAULT '',
    owner_id UUID NOT NULL,
    owner_username VARCHAR(32) NOT NULL,
    resource_label TEXT NOT NULL,
    service_type VARCHAR(63) NOT NULL DEFAULT '',
    environment_name TEXT NOT NULL DEFAULT '',
    environment_ip INET,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE NULLS NOT DISTINCT(thread_id, resource_type, resource_id, resource_key)
);
CREATE INDEX idx_communication_resources_thread ON communication_resource_refs(thread_id, created_at, id);

CREATE TABLE models (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    marker_owner_id UUID NOT NULL,
    environment_id UUID NOT NULL REFERENCES environments(id) ON DELETE RESTRICT,
    environment_name TEXT NOT NULL,
    environment_ip INET NOT NULL,
    name TEXT NOT NULL,
    source VARCHAR(16) NOT NULL CHECK(source IN ('offline', 'modelscope', 'huggingface')),
    target_dir TEXT NOT NULL,
    original_filename TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    expanded_size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (expanded_size_bytes >= 0),
    file_count INTEGER NOT NULL DEFAULT 0 CHECK (file_count >= 0),
    sha256 CHAR(64),
    status VARCHAR(32) NOT NULL,
    error_message TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_by_username VARCHAR(32) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    ready_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_models_owner_target_active
    ON models(owner_id, environment_ip, target_dir) WHERE deleted_at IS NULL;
CREATE INDEX idx_models_owner_created ON models(owner_id, created_at DESC);
CREATE INDEX idx_models_environment_active ON models(environment_id) WHERE deleted_at IS NULL;

CREATE TABLE model_uploads (
    id UUID PRIMARY KEY,
    model_id UUID NOT NULL UNIQUE REFERENCES models(id) ON DELETE CASCADE,
    owner_id UUID NOT NULL,
    remote_path TEXT NOT NULL,
    offset_bytes BIGINT NOT NULL DEFAULT 0 CHECK (offset_bytes >= 0),
    total_bytes BIGINT NOT NULL CHECK (total_bytes >= 0),
    status VARCHAR(32) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (offset_bytes <= total_bytes)
);
CREATE INDEX idx_model_uploads_expiry ON model_uploads(status, expires_at);

CREATE TABLE model_tasks (
    id UUID PRIMARY KEY,
    model_id UUID NOT NULL REFERENCES models(id) ON DELETE RESTRICT,
    owner_id UUID NOT NULL,
    actor_user_id UUID,
    actor_username VARCHAR(32) NOT NULL DEFAULT '',
    action VARCHAR(16) NOT NULL CHECK(action IN ('deploy', 'delete')),
    status VARCHAR(32) NOT NULL,
    stage TEXT NOT NULL DEFAULT '',
    progress INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    log_path TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_model_tasks_model_created ON model_tasks(model_id, created_at DESC);

INSERT INTO roles (id, key, name, description, system, created_at, updated_at) VALUES
    ('00000000-0000-4000-8000-000000000101', 'super_admin', '超级管理员', '系统所有者，拥有全部权限', TRUE, now(), now()),
    ('00000000-0000-4000-8000-000000000102', 'platform_admin', '平台管理员', '负责平台、账号及全部业务资源治理', TRUE, now(), now()),
    ('00000000-0000-4000-8000-000000000103', 'operator', '运维人员', '管理本人拥有的部署资源', TRUE, now(), now()),
    ('00000000-0000-4000-8000-000000000104', 'viewer', '只读用户', '查看本人拥有或参与的资源', TRUE, now(), now());

INSERT INTO permissions (key, resource, action, description, scoped) VALUES
    ('dashboard.read', 'dashboard', 'read', '查看管理总览', FALSE),
    ('account.read', 'account', 'read', '查看账号和会话', FALSE),
    ('account.create', 'account', 'create', '创建账号', FALSE),
    ('account.update', 'account', 'update', '修改账号安全状态', FALSE),
    ('account.delete', 'account', 'delete', '删除账号', FALSE),
    ('account.assign_roles', 'account', 'assign_roles', '分配用户角色', FALSE),
    ('account.transfer', 'account', 'transfer', '交接账号资源', FALSE),
    ('role.read', 'role', 'read', '查看角色和权限', FALSE),
    ('role.create', 'role', 'create', '创建角色', FALSE),
    ('role.update', 'role', 'update', '修改角色和权限绑定', FALSE),
    ('role.delete', 'role', 'delete', '删除角色', FALSE),
    ('package.read', 'package', 'read', '查看安装包', TRUE),
    ('package.write', 'package', 'write', '上传和更新安装包', TRUE),
    ('package.delete', 'package', 'delete', '删除安装包', TRUE),
    ('environment.read', 'environment', 'read', '查看环境', TRUE),
    ('environment.write', 'environment', 'write', '创建和修改环境', TRUE),
    ('environment.delete', 'environment', 'delete', '删除环境', TRUE),
    ('environment.validate', 'environment', 'validate', '校验 SSH 环境', TRUE),
    ('environment.import', 'environment', 'import', '导入环境', TRUE),
    ('environment.export', 'environment', 'export', '导出环境', TRUE),
    ('tag.read', 'tag', 'read', '查看标签', TRUE),
    ('tag.write', 'tag', 'write', '管理标签', TRUE),
    ('model.read', 'model', 'read', '查看模型', TRUE),
    ('model.upload', 'model', 'upload', '上传和重试模型', TRUE),
    ('model.delete', 'model', 'delete', '删除模型', TRUE),
    ('service.read', 'service', 'read', '查看服务', TRUE),
    ('service.config.read', 'service', 'config.read', '查看服务配置', TRUE),
    ('service.config.write', 'service', 'config.write', '修改服务配置', TRUE),
    ('service.install', 'service', 'install', '安装服务', TRUE),
    ('service.start', 'service', 'start', '启动服务', TRUE),
    ('service.stop', 'service', 'stop', '停止服务', TRUE),
    ('service.reset', 'service', 'reset', '重置服务', TRUE),
    ('service.health', 'service', 'health', '手动检查服务健康', TRUE),
    ('service.log.read', 'service', 'log.read', '查看服务日志', TRUE),
    ('operation.read', 'operation', 'read', '查看操作和日志', TRUE),
    ('audit.read', 'audit', 'read', '查看审计日志', FALSE),
    ('audit.export', 'audit', 'export', '导出审计日志', FALSE),
    ('notification.read', 'notification', 'read', '查看风险通知', FALSE),
    ('notification.update', 'notification', 'update', '处理风险通知', FALSE),
    ('communication.read', 'communication', 'read', '查看通讯事项', TRUE),
    ('communication.create', 'communication', 'create', '创建通讯事项', TRUE),
    ('communication.reply', 'communication', 'reply', '回复和标记通讯已读', TRUE),
    ('communication.manage', 'communication', 'manage', '关闭和重新打开通讯', TRUE);

INSERT INTO role_permissions (role_id, permission_id, scope)
SELECT '00000000-0000-4000-8000-000000000101', id, 'all' FROM permissions;

INSERT INTO role_permissions (role_id, permission_id, scope)
SELECT '00000000-0000-4000-8000-000000000102', id, 'all' FROM permissions;

INSERT INTO role_permissions (role_id, permission_id, scope)
SELECT '00000000-0000-4000-8000-000000000103', id, 'own'
FROM permissions
WHERE resource IN ('package', 'environment', 'tag', 'model', 'service', 'operation', 'communication');

INSERT INTO role_permissions (role_id, permission_id, scope)
SELECT '00000000-0000-4000-8000-000000000104', id, 'own'
FROM permissions
WHERE key IN (
    'package.read', 'environment.read', 'tag.read', 'model.read', 'service.read',
    'service.config.read', 'service.log.read', 'operation.read', 'communication.read'
);
