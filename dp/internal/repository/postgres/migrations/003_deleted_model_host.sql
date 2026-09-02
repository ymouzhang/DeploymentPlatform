ALTER TABLE models DROP CONSTRAINT models_host_id_fkey;
ALTER TABLE models ALTER COLUMN host_id DROP NOT NULL;
ALTER TABLE models ADD CONSTRAINT models_host_id_fkey
    FOREIGN KEY (host_id) REFERENCES hosts(id) ON DELETE SET NULL;
