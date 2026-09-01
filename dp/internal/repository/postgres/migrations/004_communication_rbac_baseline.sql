DELETE FROM role_permissions rp
USING roles r, permissions p
WHERE rp.role_id = r.id
  AND rp.permission_id = p.id
  AND r.key = 'operator'
  AND p.key IN ('communication.create', 'communication.manage');

ALTER TABLE communication_messages DROP CONSTRAINT communication_messages_type_check;
ALTER TABLE communication_messages ADD CONSTRAINT communication_messages_type_check
    CHECK (type IN ('admin_message', 'user_receipt', 'system_closed', 'system_reopened'));
