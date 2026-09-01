INSERT INTO users (
    id,
    username,
    password_hash,
    enabled,
    must_change_password,
    is_initial_admin,
    created_at,
    updated_at
) VALUES (
    '00000000-0000-4000-8000-000000000001',
    '__dp_pending_admin__',
    '',
    TRUE,
    TRUE,
    TRUE,
    now(),
    now()
);

INSERT INTO user_roles (user_id, role_id, assigned_by, assigned_at) VALUES (
    '00000000-0000-4000-8000-000000000001',
    '00000000-0000-4000-8000-000000000101',
    NULL,
    now()
);
