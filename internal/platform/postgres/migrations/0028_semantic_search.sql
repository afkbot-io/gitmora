-- Upgrade prerequisite: the database administrator installs pgvector before migrating.
-- New bundled installations enable it in 10-workgraph.sql. No source bytes are changed.
CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;
CREATE TABLE retrieval_embeddings (
 chunk_id text NOT NULL REFERENCES retrieval_chunks(id) ON DELETE CASCADE,
 profile text NOT NULL CHECK(length(profile)=64),
 embedding public.vector,
 completed_at timestamptz,
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 lease_owner uuid,
 lease_until timestamptz,
 last_error_code text,
 PRIMARY KEY(profile,chunk_id),
 CHECK ((embedding IS NULL)=(completed_at IS NULL))
);
CREATE INDEX retrieval_embeddings_due ON retrieval_embeddings(profile,available_at,chunk_id)
 WHERE completed_at IS NULL;
CREATE INDEX retrieval_chunks_content_digest ON retrieval_chunks(content_digest,job_id,id);
CREATE TABLE retrieval_search_snapshots (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 query_digest text NOT NULL,
 profile text NOT NULL,
 vector_digest text NOT NULL,
 engine text NOT NULL CHECK(engine IN ('hybrid','semantic')),
 indexed_before timestamptz NOT NULL,
 items jsonb NOT NULL CHECK(jsonb_typeof(items)='array' AND jsonb_array_length(items)<=400),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '15 minutes'
);
CREATE INDEX retrieval_search_snapshots_user ON retrieval_search_snapshots(user_id,created_at DESC);
CREATE INDEX retrieval_search_snapshots_expiry ON retrieval_search_snapshots(expires_at);
-- Exact cosine retrieval is scoped by current permissions before ranking. Different
-- model/dimension profiles never share a distance calculation or an approximate index.
