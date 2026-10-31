ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check CHECK(cardinality(scopes) BETWEEN 1 AND 7 AND scopes <@ ARRAY['capture:read','capture:write','integration:read','integration:manage','knowledge:read','knowledge:write','knowledge:review']::text[]);

CREATE TABLE knowledge_entries(
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,id uuid NOT NULL,
 repository_id uuid REFERENCES repositories(id) ON DELETE CASCADE,country_id uuid,task_id uuid,commit_sha text,
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),input_hash text NOT NULL CHECK(input_hash ~ '^[0-9a-f]{64}$'),
 model jsonb NOT NULL CHECK(jsonb_typeof(model)='object' AND octet_length(model::text)<=70000),
 reference_count smallint NOT NULL CHECK(reference_count BETWEEN 1 AND 16),
 created_by uuid NOT NULL REFERENCES users(id),created_at timestamptz NOT NULL DEFAULT now(),
 state text NOT NULL DEFAULT 'proposed' CHECK(state IN('proposed','reviewed','superseded','retracted')),
 reviewed_by uuid REFERENCES users(id),reviewed_at timestamptz,recheck_at timestamptz,superseded_by uuid,
 PRIMARY KEY(project_id,id),UNIQUE(project_id,id,digest),
 CHECK((country_id IS NULL)=(task_id IS NULL)),
 CHECK((reviewed_at IS NULL)=(reviewed_by IS NULL) AND (reviewed_at IS NULL)=(recheck_at IS NULL)),
 CHECK(state<>'reviewed' OR (reviewed_at IS NOT NULL AND reviewed_by<>created_by)),
 CHECK((state='superseded')=(superseded_by IS NOT NULL))
);
CREATE INDEX knowledge_entries_state ON knowledge_entries(project_id,state,id);
CREATE TABLE knowledge_references(
 project_id uuid NOT NULL,entry_id uuid NOT NULL,ordinal smallint NOT NULL CHECK(ordinal BETWEEN 0 AND 15),
 evidence_id text NOT NULL CHECK(evidence_id ~ '^[0-9a-f]{64}$'),content_digest text NOT NULL CHECK(content_digest ~ '^[0-9a-f]{64}$'),source_digest text NOT NULL CHECK(source_digest ~ '^[0-9a-f]{64}$'),source_kind text NOT NULL CHECK(source_kind IN('capture','task','code')),
 PRIMARY KEY(project_id,entry_id,ordinal),FOREIGN KEY(project_id,entry_id) REFERENCES knowledge_entries(project_id,id) ON DELETE CASCADE
);
CREATE INDEX knowledge_references_evidence ON knowledge_references(evidence_id,project_id,entry_id);
CREATE TABLE knowledge_events(
 project_id uuid NOT NULL,request_id uuid NOT NULL,entry_id uuid NOT NULL,request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 actor_id uuid NOT NULL REFERENCES users(id),command jsonb NOT NULL,decision jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(project_id,request_id),FOREIGN KEY(project_id,entry_id) REFERENCES knowledge_entries(project_id,id) ON DELETE CASCADE
);
CREATE INDEX knowledge_events_entry ON knowledge_events(project_id,entry_id,created_at,request_id);

ALTER TABLE retrieval_jobs ADD COLUMN knowledge_id uuid;
ALTER TABLE retrieval_jobs ADD FOREIGN KEY(project_id,knowledge_id,source_digest) REFERENCES knowledge_entries(project_id,id,digest) ON DELETE CASCADE;
ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_source_kind_check;
ALTER TABLE retrieval_jobs DROP CONSTRAINT retrieval_jobs_shape;
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_source_kind_check CHECK(source_kind IN('capture','task','code','knowledge'));
ALTER TABLE retrieval_jobs ADD CONSTRAINT retrieval_jobs_shape CHECK(
 (source_kind='capture' AND repository_id IS NOT NULL AND session_id IS NOT NULL AND revision IS NOT NULL AND country_id IS NULL AND task_id IS NULL AND code_sha IS NULL AND code_path IS NULL AND code_blob_id IS NULL AND knowledge_id IS NULL)
 OR(source_kind='task' AND repository_id IS NULL AND session_id IS NULL AND revision IS NULL AND country_id IS NOT NULL AND task_id IS NOT NULL AND code_sha IS NULL AND code_path IS NULL AND code_blob_id IS NULL AND knowledge_id IS NULL)
 OR(source_kind='code' AND repository_id IS NOT NULL AND session_id IS NULL AND revision IS NULL AND country_id IS NULL AND task_id IS NULL AND code_sha IS NOT NULL AND code_path IS NOT NULL AND code_blob_id IS NOT NULL AND knowledge_id IS NULL)
 OR(source_kind='knowledge' AND session_id IS NULL AND revision=1 AND country_id IS NULL AND task_id IS NULL AND code_path IS NULL AND code_blob_id IS NULL AND knowledge_id IS NOT NULL AND (code_sha IS NULL OR repository_id IS NOT NULL))
);
ALTER TABLE retrieval_chunks DROP CONSTRAINT retrieval_chunks_kind_check;
ALTER TABLE retrieval_chunks ADD CONSTRAINT retrieval_chunks_kind_check CHECK(kind IN('session','task','document','code_reference','code','knowledge'));
