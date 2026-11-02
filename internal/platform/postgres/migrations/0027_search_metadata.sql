-- Derived search metadata can be rebuilt without modifying any immutable capture bytes.
ALTER TABLE retrieval_chunks ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}';
ALTER TABLE retrieval_chunks ADD COLUMN file_paths text[] NOT NULL DEFAULT '{}';
ALTER TABLE retrieval_chunks ADD COLUMN part_lexical tsvector;
ALTER TABLE retrieval_chunks ADD CONSTRAINT retrieval_metadata_object CHECK(jsonb_typeof(metadata)='object');
CREATE INDEX retrieval_chunks_agent ON retrieval_chunks(lower(metadata->>'agent'), job_id);
CREATE INDEX retrieval_chunks_model ON retrieval_chunks(lower(metadata->>'model'), job_id);
CREATE INDEX retrieval_chunks_paths ON retrieval_chunks USING gin(file_paths);
CREATE UNIQUE INDEX retrieval_chunks_part_paths ON retrieval_chunks(job_id,part) WHERE cardinality(file_paths)>0;
CREATE INDEX retrieval_chunks_message_lexical ON retrieval_chunks USING gin(part_lexical) WHERE part_lexical IS NOT NULL;
-- Keep acknowledged text available until each immutable source is atomically reprojected.
ALTER TABLE retrieval_jobs ADD COLUMN projection_version integer NOT NULL DEFAULT 1 CHECK(projection_version>0);
CREATE INDEX retrieval_jobs_upgrade_due ON retrieval_jobs(available_at,id) WHERE completed_at IS NULL OR projection_version<2;
