CREATE TABLE retrieval_jobs (
 id uuid PRIMARY KEY,
 source_kind text NOT NULL CHECK(source_kind IN('capture','task')),
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 repository_id uuid REFERENCES repositories(id) ON DELETE CASCADE,
 session_id uuid,revision bigint,country_id uuid,task_id uuid,
 source_digest text NOT NULL CHECK(source_digest ~ '^[0-9a-f]{64}$'),
 source_observed_at timestamptz NOT NULL,
 outbox_id bigint REFERENCES outbox_events(id) ON DELETE SET NULL,
 available_at timestamptz NOT NULL DEFAULT now(),lease_owner uuid,lease_until timestamptz,
 attempts integer NOT NULL DEFAULT 0,last_error_code text,completed_at timestamptz,
 UNIQUE NULLS NOT DISTINCT(source_kind,project_id,repository_id,session_id,revision,country_id,task_id,source_digest),
 FOREIGN KEY(repository_id,session_id,revision) REFERENCES capture_revisions(repository_id,session_id,revision) ON DELETE CASCADE,
 FOREIGN KEY(project_id,task_id,source_digest) REFERENCES work_task_observations(project_id,task_id,digest) ON DELETE CASCADE,
 CHECK((source_kind='capture' AND repository_id IS NOT NULL AND session_id IS NOT NULL AND revision IS NOT NULL AND country_id IS NULL AND task_id IS NULL)
  OR(source_kind='task' AND repository_id IS NULL AND session_id IS NULL AND revision IS NULL AND country_id IS NOT NULL AND task_id IS NOT NULL))
);
CREATE INDEX retrieval_jobs_due ON retrieval_jobs(available_at,id) WHERE completed_at IS NULL;

CREATE TABLE retrieval_chunks (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{64}$'),
 job_id uuid NOT NULL REFERENCES retrieval_jobs(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN('session','task','document','code_reference')),
 part text NOT NULL CHECK(length(part) BETWEEN 1 AND 200),
 start_rune integer NOT NULL CHECK(start_rune>=0),end_rune integer NOT NULL CHECK(end_rune>start_rune AND end_rune-start_rune<=2048),
 title text NOT NULL CHECK(octet_length(title) BETWEEN 1 AND 2048),
 content text NOT NULL CHECK(octet_length(content) BETWEEN 1 AND 8192),
 content_digest text NOT NULL CHECK(content_digest ~ '^[0-9a-f]{64}$'),
 country_id uuid,task_id uuid,
 title_terms text NOT NULL,content_terms text NOT NULL,
 lexical tsvector GENERATED ALWAYS AS (
  setweight(to_tsvector('simple',title_terms)||to_tsvector('english',title_terms)||to_tsvector('russian',title_terms),'A') ||
  setweight(to_tsvector('simple',content_terms)||to_tsvector('english',content_terms)||to_tsvector('russian',content_terms),'B')
 ) STORED,
 indexed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX retrieval_chunks_lexical ON retrieval_chunks USING gin(lexical);
CREATE INDEX retrieval_chunks_job ON retrieval_chunks(job_id,kind,id);
CREATE INDEX retrieval_chunks_binding ON retrieval_chunks(country_id,task_id,job_id);

-- Existing immutable sources are backfilled; completion remains empty until the real worker runs.
INSERT INTO retrieval_jobs(id,source_kind,project_id,repository_id,session_id,revision,source_digest,source_observed_at,outbox_id)
 SELECT gen_random_uuid(),'capture',r.project_id,cr.repository_id,cr.session_id,cr.revision,cr.digest,cr.created_at,e.id
 FROM capture_revisions cr JOIN repositories r ON r.id=cr.repository_id LEFT JOIN outbox_events e
 ON e.repository_id=cr.repository_id AND e.event_key='capture:'||cr.session_id::text||':'||cr.revision::text AND e.event_type='capture.revision_recorded';
INSERT INTO retrieval_jobs(id,source_kind,project_id,country_id,task_id,source_digest,source_observed_at)
 SELECT gen_random_uuid(),'task',project_id,country_id,task_id,digest,observed_at FROM work_task_observations;
