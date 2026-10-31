ALTER TABLE forge_connections ADD COLUMN metadata_lane integer NOT NULL DEFAULT 0 CHECK(metadata_lane BETWEEN 0 AND 4);
CREATE TABLE forge_sync_streams(
 repository_id uuid NOT NULL REFERENCES forge_connections(repository_id) ON DELETE CASCADE,
 kind text NOT NULL CHECK(kind IN('merge_requests','pipelines','child_pipelines','deployments')),
 version bigint NOT NULL DEFAULT 1,
 watermark timestamptz NOT NULL DEFAULT '1970-01-01 UTC',
 cycle_after timestamptz,cycle_before timestamptz,cycle_full boolean NOT NULL DEFAULT true,
 next_page integer NOT NULL DEFAULT 1 CHECK(next_page BETWEEN 1 AND 1000000),
 last_complete_at timestamptz,full_due_at timestamptz NOT NULL DEFAULT now(),
 retry_at timestamptz NOT NULL DEFAULT '1970-01-01 UTC',last_error_code text,
 refresh_requested boolean NOT NULL DEFAULT false,
 PRIMARY KEY(repository_id,kind)
);
INSERT INTO forge_sync_streams(repository_id,kind) SELECT c.repository_id,k.kind FROM forge_connections c CROSS JOIN (VALUES('merge_requests'),('pipelines'),('child_pipelines'),('deployments')) k(kind);

CREATE TABLE forge_mr_codes(
 repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
 iid bigint NOT NULL CHECK(iid>0),code_key text NOT NULL CHECK(code_key ~ '^[0-9a-f]{64}$'),
 complete boolean NOT NULL DEFAULT false,
 PRIMARY KEY(repository_id,iid,code_key)
);
CREATE TABLE forge_mr_observations(
 repository_id uuid NOT NULL,iid bigint NOT NULL,digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 code_key text NOT NULL,model jsonb NOT NULL,provider_updated_at timestamptz NOT NULL,observed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(repository_id,iid,digest),
 FOREIGN KEY(repository_id,iid,code_key) REFERENCES forge_mr_codes(repository_id,iid,code_key) ON DELETE CASCADE
);
CREATE TABLE forge_merge_requests(
 repository_id uuid NOT NULL,iid bigint NOT NULL,digest text NOT NULL,provider_updated_at timestamptz NOT NULL,
 available boolean NOT NULL DEFAULT true,
 PRIMARY KEY(repository_id,iid),FOREIGN KEY(repository_id,iid,digest) REFERENCES forge_mr_observations(repository_id,iid,digest) ON DELETE CASCADE
);
CREATE TABLE forge_mr_jobs(
 repository_id uuid NOT NULL REFERENCES forge_connections(repository_id) ON DELETE CASCADE,
 iid bigint NOT NULL CHECK(iid>0),version bigint NOT NULL DEFAULT 1,requested_seq bigint NOT NULL DEFAULT 1,read_seq bigint NOT NULL DEFAULT 0,
 stage text NOT NULL DEFAULT 'detail' CHECK(stage IN('detail','commits')),
 next_page integer NOT NULL DEFAULT 1 CHECK(next_page BETWEEN 1 AND 1000000),expected jsonb,available_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(repository_id,iid)
);
CREATE INDEX forge_mr_jobs_due ON forge_mr_jobs(repository_id,available_at,iid);
CREATE TABLE forge_mr_commits(
 repository_id uuid NOT NULL,iid bigint NOT NULL,code_key text NOT NULL,
 source_project_id bigint NOT NULL CHECK(source_project_id>=0),sha text NOT NULL CHECK(sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
 PRIMARY KEY(repository_id,iid,code_key,sha),FOREIGN KEY(repository_id,iid,code_key) REFERENCES forge_mr_codes(repository_id,iid,code_key) ON DELETE CASCADE
);
CREATE TABLE forge_commit_lineage(
 repository_id uuid NOT NULL,iid bigint NOT NULL,code_key text NOT NULL,source_project_id bigint NOT NULL,
 source_sha text NOT NULL,result_sha text NOT NULL,kind text NOT NULL CHECK(kind IN('merge','squash')),
 PRIMARY KEY(repository_id,iid,code_key,source_sha,result_sha,kind),
 FOREIGN KEY(repository_id,iid,code_key) REFERENCES forge_mr_codes(repository_id,iid,code_key) ON DELETE CASCADE
);
CREATE TABLE forge_pipelines(
 repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,id bigint NOT NULL CHECK(id>0),
 sha text NOT NULL,model jsonb NOT NULL,provider_updated_at timestamptz NOT NULL,
 PRIMARY KEY(repository_id,id)
);
CREATE INDEX forge_pipelines_sha ON forge_pipelines(repository_id,sha,id);
CREATE TABLE forge_deployments(
 repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,id bigint NOT NULL CHECK(id>0),
 sha text NOT NULL,model jsonb NOT NULL,provider_updated_at timestamptz NOT NULL,
 PRIMARY KEY(repository_id,id)
);
CREATE INDEX forge_deployments_sha ON forge_deployments(repository_id,sha,id);
