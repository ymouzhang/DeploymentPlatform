-- Creating and managing communication threads are coordinator-wide actions.
-- Remove stale own-scope grants before marking these permissions global-only.
DELETE FROM role_permissions
WHERE permission_id IN (
    SELECT id FROM permissions
    WHERE key IN ('communication.create', 'communication.manage')
)
AND scope <> 'all';

UPDATE permissions
SET scoped = FALSE
WHERE key IN ('communication.create', 'communication.manage');
