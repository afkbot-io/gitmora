CREATE TABLE work_connections (
 project_id uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
 endpoint text NOT NULL, country_id uuid NOT NULL,
 secret_envelope bytea, enabled boolean NOT NULL DEFAULT true,
 generation bigint NOT NULL DEFAULT 1, blocked boolean NOT NULL DEFAULT false,
 last_error_code text, updated_by uuid NOT NULL REFERENCES users(id), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(project_id,country_id), CHECK(NOT enabled OR (secret_envelope IS NOT NULL AND octet_length(secret_envelope) BETWEEN 32 AND 16640))
);
CREATE TABLE work_task_observations (
 project_id uuid NOT NULL, country_id uuid NOT NULL, task_id uuid NOT NULL,
 model jsonb NOT NULL, digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),
 observed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(project_id,task_id,digest), FOREIGN KEY(project_id,country_id) REFERENCES work_connections(project_id,country_id) ON DELETE CASCADE
);
CREATE TABLE work_tasks (
 project_id uuid NOT NULL,country_id uuid NOT NULL,task_id uuid NOT NULL,digest text NOT NULL,
 available boolean NOT NULL DEFAULT true,observed_at timestamptz NOT NULL DEFAULT now(),read_started_at timestamptz NOT NULL,
 PRIMARY KEY(project_id,task_id),FOREIGN KEY(project_id,country_id) REFERENCES work_connections(project_id,country_id) ON DELETE CASCADE,
 FOREIGN KEY(project_id,task_id,digest) REFERENCES work_task_observations(project_id,task_id,digest)
);
CREATE TABLE work_jobs (
 id uuid PRIMARY KEY, project_id uuid NOT NULL, country_id uuid NOT NULL, task_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN('refresh','publication')), request_key text NOT NULL,
 document jsonb, state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','blocked','complete')),
 available_at timestamptz NOT NULL DEFAULT now(), lease_owner uuid, lease_until timestamptz,
 requested_seq bigint NOT NULL DEFAULT 1, read_seq bigint NOT NULL DEFAULT 0,
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0), last_error_code text, completed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(project_id,request_key), FOREIGN KEY(project_id,country_id) REFERENCES work_connections(project_id,country_id) ON DELETE CASCADE,
 CHECK((kind='publication')=(document IS NOT NULL))
);
CREATE INDEX work_jobs_due ON work_jobs(available_at,id) WHERE state='pending';
