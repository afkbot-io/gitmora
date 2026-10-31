ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check CHECK(cardinality(scopes) BETWEEN 1 AND 9 AND scopes <@ ARRAY['capture:read','capture:write','integration:read','integration:manage','knowledge:read','knowledge:write','knowledge:review','review:read','review:run']::text[]);

CREATE TABLE review_runs (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 token_id uuid NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
 scopes text[] NOT NULL,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 scope jsonb NOT NULL CHECK(jsonb_typeof(scope)='object'),
 configuration jsonb NOT NULL CHECK(jsonb_typeof(configuration)='object'),
 source jsonb CHECK(source IS NULL OR (jsonb_typeof(source)='object' AND octet_length(source::text)<=2200000)),
 report jsonb CHECK(report IS NULL OR (jsonb_typeof(report)='object' AND octet_length(report::text)<=75000)),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','running','succeeded','stale','failed','blocked')),
 diagnostic text NOT NULL DEFAULT '' CHECK(diagnostic='' OR diagnostic ~ '^[a-z_]{1,48}$'),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),
 generation bigint NOT NULL DEFAULT 0,
 lease_owner uuid,lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id,request_id),
 CHECK((status='running')=(lease_owner IS NOT NULL AND lease_until IS NOT NULL)),
 CHECK(status!='succeeded' OR report IS NOT NULL),
 CHECK(report IS NULL OR source IS NOT NULL)
);
CREATE INDEX review_ready_idx ON review_runs(next_attempt_at,created_at) WHERE status IN('pending','running');
CREATE INDEX review_repository_idx ON review_runs(repository_id,created_at DESC,id);
