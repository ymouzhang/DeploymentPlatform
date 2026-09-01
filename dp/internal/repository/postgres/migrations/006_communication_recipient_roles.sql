ALTER TABLE communication_message_recipients
    ADD COLUMN recipient_role_keys JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE communication_message_recipients recipients
SET recipient_role_keys = roles.role_keys
FROM (
    SELECT ur.user_id, jsonb_agg(r.key ORDER BY r.key) AS role_keys
    FROM user_roles ur
    JOIN roles r ON r.id = ur.role_id
    GROUP BY ur.user_id
) roles
WHERE roles.user_id = recipients.recipient_user_id;
