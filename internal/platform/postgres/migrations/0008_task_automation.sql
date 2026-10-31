ALTER TABLE work_connections ADD COLUMN auto_link_merge_requests boolean NOT NULL DEFAULT false;
ALTER TABLE work_connections ADD COLUMN discovery_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE work_jobs DROP CONSTRAINT work_jobs_kind_check;
ALTER TABLE work_jobs ADD CONSTRAINT work_jobs_kind_check CHECK(kind IN('refresh','publication','link'));
ALTER TABLE work_jobs ADD COLUMN mr_link jsonb;
ALTER TABLE work_jobs ADD COLUMN read_started_at timestamptz;
UPDATE work_jobs SET lease_owner=NULL,lease_until=NULL;
ALTER TABLE work_jobs ADD CONSTRAINT work_jobs_read_fence CHECK(lease_owner IS NULL OR read_started_at IS NOT NULL);
ALTER TABLE work_jobs ADD CONSTRAINT work_jobs_link_payload CHECK((kind='link')=(mr_link IS NOT NULL));

CREATE TABLE work_capture_bindings(
 repository_id uuid NOT NULL,session_id uuid NOT NULL,country_id uuid NOT NULL,task_id uuid NOT NULL,
 PRIMARY KEY(repository_id,session_id),
 FOREIGN KEY(repository_id,session_id) REFERENCES capture_sessions(repository_id,session_id) ON DELETE CASCADE
);
CREATE INDEX work_capture_bindings_task ON work_capture_bindings(country_id,task_id,repository_id);
INSERT INTO work_capture_bindings(repository_id,session_id,country_id,task_id)
 SELECT DISTINCT ON(repository_id,session_id) repository_id,session_id,
 (convert_from(canonical,'UTF8')::jsonb->'task'->>'country_id')::uuid,
 (convert_from(canonical,'UTF8')::jsonb->'task'->>'task_id')::uuid
 FROM capture_revisions WHERE convert_from(canonical,'UTF8')::jsonb->'task'->>'task_id' IS NOT NULL
 ORDER BY repository_id,session_id,revision;

CREATE TABLE work_link_evidence(
 job_id uuid NOT NULL REFERENCES work_jobs(id) ON DELETE CASCADE,
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),model jsonb NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(job_id,digest)
);
CREATE INDEX forge_mr_commits_source ON forge_mr_commits(source_project_id,sha,repository_id,iid,code_key);
