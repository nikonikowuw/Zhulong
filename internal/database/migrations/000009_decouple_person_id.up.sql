-- Backup persons and person_faces before table recreation so that
-- foreign key CASCADE delete does not purge enrolled face records.
CREATE TABLE temp_persons AS SELECT * FROM persons;
CREATE TABLE temp_person_faces AS SELECT * FROM person_faces;

DROP TABLE persons;

CREATE TABLE persons (
    id TEXT PRIMARY KEY NOT NULL,
    -- person_id is the external business key: optional, nullable, and unique when assigned.
    person_id TEXT,
    name TEXT NOT NULL,
    primary_face_id TEXT REFERENCES person_faces(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Clean historical fake person_id values (where person_id == id) to NULL
INSERT INTO persons (id, person_id, name, primary_face_id, created_at, updated_at)
SELECT
    id,
    CASE WHEN person_id = id THEN NULL ELSE person_id END,
    name,
    primary_face_id,
    created_at,
    updated_at
FROM temp_persons;

-- Restore face templates in case cascade delete emptied them
INSERT OR IGNORE INTO person_faces (id, person_id, image_path, feature_vector, created_at)
SELECT id, person_id, image_path, feature_vector, created_at FROM temp_person_faces;

DROP TABLE temp_persons;
DROP TABLE temp_person_faces;

CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_person_id ON persons(person_id) WHERE person_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_persons_primary_face_id ON persons(primary_face_id);
