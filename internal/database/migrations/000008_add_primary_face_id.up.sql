ALTER TABLE persons ADD COLUMN primary_face_id TEXT REFERENCES person_faces(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_persons_primary_face_id ON persons(primary_face_id);
