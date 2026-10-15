CREATE TABLE temp_persons AS SELECT * FROM persons;
CREATE TABLE temp_person_faces AS SELECT * FROM person_faces;

DROP TABLE persons;

CREATE TABLE persons (
    id TEXT PRIMARY KEY NOT NULL,
    person_id TEXT NOT NULL CHECK (person_id <> ''),
    name TEXT NOT NULL,
    primary_face_id TEXT REFERENCES person_faces(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO persons (id, person_id, name, primary_face_id, created_at, updated_at)
SELECT
    id,
    COALESCE(person_id, id),
    name,
    primary_face_id,
    created_at,
    updated_at
FROM temp_persons;

INSERT OR IGNORE INTO person_faces (id, person_id, image_path, feature_vector, created_at)
SELECT id, person_id, image_path, feature_vector, created_at FROM temp_person_faces;

DROP TABLE temp_persons;
DROP TABLE temp_person_faces;

CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_person_id ON persons(person_id);
CREATE INDEX IF NOT EXISTS idx_persons_primary_face_id ON persons(primary_face_id);
