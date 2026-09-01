ALTER TABLE models DROP CONSTRAINT models_environment_id_fkey;
ALTER TABLE models ALTER COLUMN environment_id DROP NOT NULL;
ALTER TABLE models ADD CONSTRAINT models_environment_id_fkey
    FOREIGN KEY (environment_id) REFERENCES environments(id) ON DELETE SET NULL;
