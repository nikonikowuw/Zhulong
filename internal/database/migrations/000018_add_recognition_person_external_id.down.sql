DROP INDEX IF EXISTS idx_recognitions_person_external;
ALTER TABLE recognitions DROP COLUMN person_external_id;
