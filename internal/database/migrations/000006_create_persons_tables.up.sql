CREATE TABLE IF NOT EXISTS persons (
    id TEXT PRIMARY KEY NOT NULL,
    -- person_id is the business key handed to external systems: mandatory,
    -- unique and immutable. The surrogate id above stays internal.
    person_id TEXT NOT NULL CHECK (person_id <> ''),
    name TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_person_id ON persons(person_id);

CREATE TABLE IF NOT EXISTS person_faces (
    id TEXT PRIMARY KEY NOT NULL,
    person_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    image_path TEXT NOT NULL DEFAULT '',
    feature_vector BLOB NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_person_faces_person_id ON person_faces(person_id);
