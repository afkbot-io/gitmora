-- Retire remote task connections, jobs and derived search projections. Capture bytes stay immutable.
DELETE FROM retrieval_jobs WHERE source_kind='task';
DROP TABLE work_link_evidence, work_jobs, work_tasks, work_task_observations, work_capture_bindings, work_connections CASCADE;
ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_shape;
ALTER TABLE retrieval_jobs DROP COLUMN country_id, DROP COLUMN task_id;
ALTER TABLE retrieval_chunks DROP COLUMN country_id, DROP COLUMN task_id;
ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_source_kind_check;
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_source_kind_check CHECK(source_kind IN('capture','code'));
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_shape CHECK(
 (source_kind='capture' AND repository_id IS NOT NULL AND session_id IS NOT NULL AND revision IS NOT NULL AND code_sha IS NULL AND code_path IS NULL AND code_blob_id IS NULL) OR
 (source_kind='code' AND repository_id IS NOT NULL AND session_id IS NULL AND revision IS NULL AND code_sha IS NOT NULL AND code_path IS NOT NULL AND code_blob_id IS NOT NULL));
ALTER TABLE retrieval_jobs ADD UNIQUE NULLS NOT DISTINCT(source_kind,project_id,repository_id,session_id,revision,code_sha,code_path,source_digest);
ALTER TABLE retrieval_chunks DROP CONSTRAINT retrieval_chunks_kind_check;
ALTER TABLE retrieval_chunks ADD CONSTRAINT retrieval_chunks_kind_check CHECK(kind IN('session','code_reference','code'));

DROP INDEX capture_context_task;

-- Display the first recorded message without reading transcripts on every dashboard request.
ALTER TABLE capture_context_scopes ADD COLUMN title text NOT NULL DEFAULT '';
UPDATE capture_context_scopes x SET title=left(convert_from(r.canonical,'UTF8')::jsonb->'messages'->0->>'text',160)
 FROM capture_sessions s JOIN capture_revisions r ON (r.repository_id,r.session_id,r.revision)=(s.repository_id,s.session_id,s.latest_revision)
 WHERE (x.repository_id,x.session_id)=(s.repository_id,s.session_id) AND r.digest=encode(sha256(r.canonical),'hex')
 AND length(convert_from(r.canonical,'UTF8')::jsonb->'messages'->0->>'text')>0;
