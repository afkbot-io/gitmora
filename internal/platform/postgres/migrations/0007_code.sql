ALTER TABLE forge_connections ADD COLUMN code_lease_owner uuid;
ALTER TABLE forge_connections ADD COLUMN code_lease_until timestamptz;
ALTER TABLE forge_connections ADD COLUMN code_next_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE forge_connections ADD COLUMN code_probe_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE forge_connections ADD COLUMN code_checked_at timestamptz;
ALTER TABLE forge_connections ADD COLUMN code_blocked boolean NOT NULL DEFAULT false;
ALTER TABLE forge_connections ADD COLUMN code_error text;

CREATE TABLE forge_code_runs(
 repository_id uuid NOT NULL,sha text NOT NULL,
 version bigint NOT NULL DEFAULT 1,next_page integer NOT NULL DEFAULT 1,
 discovery_ended boolean NOT NULL DEFAULT false,provider_available boolean NOT NULL DEFAULT true,
 local_limit boolean NOT NULL DEFAULT false,
 lane integer NOT NULL DEFAULT 0 CHECK(lane IN(0,1)),
 available_at timestamptz NOT NULL DEFAULT now(),attempts integer NOT NULL DEFAULT 0,last_error_code text,
 PRIMARY KEY(repository_id,sha),FOREIGN KEY(repository_id,sha) REFERENCES commits(repository_id,sha) ON DELETE CASCADE
);
CREATE TABLE forge_code_paths(
 repository_id uuid NOT NULL,sha text NOT NULL,path text NOT NULL CHECK(octet_length(path) BETWEEN 1 AND 4096),
 status text NOT NULL CHECK(status IN('pending','stored','skipped','missing')),
 reason text NOT NULL DEFAULT '',observed_at timestamptz NOT NULL DEFAULT now(),retry_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(repository_id,sha,path),FOREIGN KEY(repository_id,sha) REFERENCES forge_code_runs(repository_id,sha) ON DELETE CASCADE
);
CREATE TABLE forge_code_snapshots(
 repository_id uuid NOT NULL,sha text NOT NULL,path text NOT NULL,
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),blob_id text NOT NULL,
 provider_base_url text NOT NULL,provider_project_id bigint NOT NULL,
 model jsonb NOT NULL,observed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(repository_id,sha,path),UNIQUE(repository_id,sha,path,digest),
 FOREIGN KEY(repository_id,sha,path) REFERENCES forge_code_paths(repository_id,sha,path) ON DELETE CASCADE
);
INSERT INTO forge_code_runs(repository_id,sha) SELECT repository_id,sha FROM commits WHERE confirmed_at IS NOT NULL;

ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_source_kind_check;
ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_check;
ALTER TABLE retrieval_jobs ADD COLUMN code_sha text;
ALTER TABLE retrieval_jobs ADD COLUMN code_path text;
ALTER TABLE retrieval_jobs ADD COLUMN code_blob_id text;
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_source_kind_check CHECK(source_kind IN('capture','task','code'));
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_shape CHECK(
 (source_kind='capture' AND repository_id IS NOT NULL AND session_id IS NOT NULL AND revision IS NOT NULL AND country_id IS NULL AND task_id IS NULL AND code_sha IS NULL AND code_path IS NULL AND code_blob_id IS NULL)
 OR(source_kind='task' AND repository_id IS NULL AND session_id IS NULL AND revision IS NULL AND country_id IS NOT NULL AND task_id IS NOT NULL AND code_sha IS NULL AND code_path IS NULL AND code_blob_id IS NULL)
 OR(source_kind='code' AND repository_id IS NOT NULL AND session_id IS NULL AND revision IS NULL AND country_id IS NULL AND task_id IS NULL AND code_sha IS NOT NULL AND code_path IS NOT NULL AND code_blob_id IS NOT NULL)
);
ALTER TABLE retrieval_jobs ADD FOREIGN KEY(repository_id,code_sha,code_path,source_digest) REFERENCES forge_code_snapshots(repository_id,sha,path,digest) ON DELETE CASCADE;
-- The existing source identity constraint includes the unique full projection digest.
ALTER TABLE retrieval_chunks DROP CONSTRAINT retrieval_chunks_kind_check;
ALTER TABLE retrieval_chunks ADD CONSTRAINT retrieval_chunks_kind_check CHECK(kind IN('session','task','document','code_reference','code'));
ALTER TABLE retrieval_chunks ADD COLUMN line_start integer NOT NULL DEFAULT 0;
ALTER TABLE retrieval_chunks ADD COLUMN line_end integer NOT NULL DEFAULT 0;
