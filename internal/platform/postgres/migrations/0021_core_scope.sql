-- Retire server-generated outputs; immutable captures, task/code observations and their projections survive.
DELETE FROM retrieval_jobs WHERE source_kind='knowledge';
ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_shape;
ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_source_kind_check;
ALTER TABLE retrieval_jobs DROP COLUMN knowledge_id;
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_source_kind_check CHECK(source_kind IN('capture','task','code'));
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_shape CHECK(
 (source_kind='capture' AND repository_id IS NOT NULL AND session_id IS NOT NULL AND revision IS NOT NULL AND country_id IS NULL AND task_id IS NULL AND code_sha IS NULL AND code_path IS NULL AND code_blob_id IS NULL)
 OR(source_kind='task' AND repository_id IS NULL AND session_id IS NULL AND revision IS NULL AND country_id IS NOT NULL AND task_id IS NOT NULL AND code_sha IS NULL AND code_path IS NULL AND code_blob_id IS NULL)
 OR(source_kind='code' AND repository_id IS NOT NULL AND session_id IS NULL AND revision IS NULL AND country_id IS NULL AND task_id IS NULL AND code_sha IS NOT NULL AND code_path IS NOT NULL AND code_blob_id IS NOT NULL)
);
ALTER TABLE retrieval_chunks DROP CONSTRAINT retrieval_chunks_kind_check;
ALTER TABLE retrieval_chunks ADD CONSTRAINT retrieval_chunks_kind_check CHECK(kind IN('session','task','document','code_reference','code'));
DROP TABLE inference_cancellations,review_cancellations;
DROP TABLE inference_runs,review_runs;
DROP TABLE knowledge_events,knowledge_references,knowledge_entries;

-- Strip only retired capabilities; never grant replacements. Empty grants remain revoked and empty.
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;
UPDATE api_tokens SET scopes=array_remove(array_remove(array_remove(array_remove(array_remove(scopes,
 'knowledge:read'),'knowledge:write'),'knowledge:review'),'review:read'),'review:run');
UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,now()) WHERE cardinality(scopes)=0;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check CHECK(
 (cardinality(scopes) BETWEEN 1 AND 8 OR(cardinality(scopes)=0 AND revoked_at IS NOT NULL))
 AND scopes <@ ARRAY['capture:read','capture:write','integration:read','integration:manage','directory:read','directory:manage','platform:manage','credentials:manage']::text[]);
-- Enrollment intent is immutable: cancel pending old requests rather than approve different scopes.
UPDATE identity_cli_enrollments SET status='cancelled',decided_at=now()
 WHERE status='pending' AND scopes && ARRAY['knowledge:read','knowledge:write','knowledge:review','review:read','review:run']::text[];
