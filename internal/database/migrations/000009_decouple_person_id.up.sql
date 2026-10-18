-- Backup persons and person_faces before table recreation so that
-- foreign key CASCADE delete does not purge enrolled face records.
--
-- persons and person_faces reference each other: persons.primary_face_id ->
-- person_faces.id and person_faces.person_id -> persons.id. DROP TABLE persons
-- therefore cascades into person_faces and empties it, and no single INSERT
-- order satisfies both foreign keys while the other table is still empty.
-- The cycle is broken by inserting persons with primary_face_id NULL,
-- restoring person_faces, then re-pointing primary_face_id.
--
-- `PRAGMA foreign_keys = OFF` is NOT usable here: golang-migrate executes each
-- migration inside a transaction, and SQLite ignores that pragma inside one.
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

-- Clean historical fake person_id values (where person_id == id) to NULL.
-- primary_face_id is written as NULL on purpose: DROP TABLE persons above
-- triggered ON DELETE CASCADE, so person_faces is empty at this point and any
-- non-NULL primary_face_id would violate the foreign key.
INSERT INTO persons (id, person_id, name, primary_face_id, created_at, updated_at)
SELECT
    id,
    CASE WHEN person_id = id THEN NULL ELSE person_id END,
    name,
    NULL,
    created_at,
    updated_at
FROM temp_persons;

-- Restore face templates in case cascade delete emptied them
INSERT OR IGNORE INTO person_faces (id, person_id, image_path, feature_vector, created_at)
SELECT id, person_id, image_path, feature_vector, created_at FROM temp_person_faces;

-- Re-point the primary face now that person_faces is populated again. Rows whose
-- recorded primary face no longer exists are left NULL rather than failing the
-- foreign key check.
UPDATE persons
SET primary_face_id = (
    SELECT t.primary_face_id
    FROM temp_persons t
    WHERE t.id = persons.id
      AND t.primary_face_id IS NOT NULL
      AND EXISTS (SELECT 1 FROM person_faces f WHERE f.id = t.primary_face_id)
);

DROP TABLE temp_persons;
DROP TABLE temp_person_faces;

CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_person_id ON persons(person_id) WHERE person_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_persons_primary_face_id ON persons(primary_face_id);
