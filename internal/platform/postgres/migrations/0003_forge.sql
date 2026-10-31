ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check CHECK (
 cardinality(scopes) BETWEEN 1 AND 4
 AND scopes <@ ARRAY['capture:read','capture:write','integration:read','integration:manage']::text[]
);

CREATE TABLE forge_connections (
 repository_id uuid PRIMARY KEY REFERENCES repositories(id) ON DELETE CASCADE,
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 enabled boolean NOT NULL DEFAULT true,
 webhook_mode text NOT NULL CHECK(webhook_mode IN('signature','legacy')),
 secret_envelope bytea,
 updated_by uuid NOT NULL REFERENCES users(id),
 updated_at timestamptz NOT NULL DEFAULT now(),
 requested_seq bigint NOT NULL DEFAULT 1,
 next_sync_at timestamptz NOT NULL DEFAULT now(),
 lease_owner uuid,
 lease_until timestamptz,
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 blocked boolean NOT NULL DEFAULT false,
 last_error_code text CHECK(last_error_code ~ '^[a-z_]{1,48}$'),
 last_synced_at timestamptz,
 CHECK(NOT enabled OR secret_envelope IS NOT NULL),
 CHECK((lease_owner IS NULL)=(lease_until IS NULL))
);
CREATE INDEX forge_connections_due ON forge_connections(next_sync_at,repository_id) WHERE enabled AND NOT blocked;

CREATE TABLE forge_webhook_inbox (
 repository_id uuid NOT NULL REFERENCES forge_connections(repository_id) ON DELETE CASCADE,
 delivery_key text NOT NULL CHECK(delivery_key ~ '^[0-9a-f]{64}$'),
 body_digest text NOT NULL CHECK(body_digest ~ '^[0-9a-f]{64}$'),
 event_kind text NOT NULL,
 merge_request_iid bigint,
 after_sha text,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(repository_id,delivery_key)
);

ALTER TABLE commits ADD COLUMN verification_checked_at timestamptz;
ALTER TABLE commits ADD COLUMN verify_after timestamptz NOT NULL DEFAULT now();
CREATE INDEX commits_verification_due ON commits(repository_id,verify_after,sha) WHERE confirmed_at IS NULL;
