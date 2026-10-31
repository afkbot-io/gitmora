ALTER TABLE api_tokens ADD COLUMN auth_kind text NOT NULL DEFAULT 'api' CHECK(auth_kind IN('api','browser'));
ALTER TABLE directory_events DROP CONSTRAINT directory_events_origin_check;
ALTER TABLE directory_events ADD CONSTRAINT directory_events_origin_check CHECK(origin IN('api','offline','oidc'));

CREATE TABLE identity_runtime(id integer PRIMARY KEY CHECK(id=1),configuration text NOT NULL DEFAULT '',updated_at timestamptz NOT NULL DEFAULT now());
INSERT INTO identity_runtime(id)VALUES(1);
CREATE TABLE identity_bindings(
 id uuid PRIMARY KEY,user_id uuid NOT NULL REFERENCES users(id),
 issuer text NOT NULL CHECK(octet_length(issuer) BETWEEN 1 AND 2048),subject text NOT NULL CHECK(octet_length(subject) BETWEEN 1 AND 255),
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT nextval('directory_revision'),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(issuer,subject)
);
CREATE INDEX identity_bindings_user ON identity_bindings(user_id,id);
CREATE TABLE identity_login_flows(
 state_hash text PRIMARY KEY CHECK(state_hash ~ '^[0-9a-f]{64}$'),
 browser_hash text NOT NULL CHECK(browser_hash ~ '^[0-9a-f]{64}$'),
 configuration text NOT NULL CHECK(configuration ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes'
);
CREATE INDEX identity_login_flows_expiry ON identity_login_flows(expires_at);
CREATE TABLE identity_sessions(
 id uuid PRIMARY KEY,user_id uuid NOT NULL REFERENCES users(id),
 binding_id uuid NOT NULL REFERENCES identity_bindings(id),binding_version bigint NOT NULL,
 token_id uuid NOT NULL UNIQUE REFERENCES api_tokens(id) ON DELETE CASCADE,
 cookie_hash text NOT NULL UNIQUE CHECK(cookie_hash ~ '^[0-9a-f]{64}$'),
 csrf_hash text NOT NULL CHECK(csrf_hash ~ '^[0-9a-f]{64}$'),
 configuration text NOT NULL CHECK(configuration ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),revoked_at timestamptz
);
CREATE INDEX identity_sessions_user ON identity_sessions(user_id,created_at,id);
