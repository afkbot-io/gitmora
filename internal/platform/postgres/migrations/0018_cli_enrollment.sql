ALTER TABLE identity_sessions ADD COLUMN cli_verified_at timestamptz;

CREATE TABLE identity_cli_enrollments(
 id uuid PRIMARY KEY,
 device_hash text NOT NULL UNIQUE CHECK(device_hash ~ '^[0-9a-f]{64}$'),
 proof_hash text NOT NULL UNIQUE CHECK(proof_hash ~ '^[0-9a-f]{64}$'),
 credential_hash text NOT NULL UNIQUE CHECK(credential_hash ~ '^[0-9a-f]{64}$'),
 code_hash text NOT NULL UNIQUE CHECK(code_hash ~ '^[0-9a-f]{64}$'),
 label text NOT NULL CHECK(octet_length(label) BETWEEN 1 AND 120),
 scopes text[] NOT NULL CHECK(cardinality(scopes) BETWEEN 1 AND 16),
 ttl_seconds bigint NOT NULL CHECK(ttl_seconds BETWEEN 60 AND 7776000),
 configuration text NOT NULL CHECK(configuration ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT now(),expires_at timestamptz NOT NULL DEFAULT now()+interval '10 minutes',
 status text NOT NULL DEFAULT 'pending' CHECK(status IN('pending','approved','denied','cancelled')),
 decided_by uuid REFERENCES users(id),decided_at timestamptz,
 token_id uuid UNIQUE REFERENCES api_tokens(id),credential_expires_at timestamptz,
 CHECK((status='pending' AND decided_by IS NULL AND decided_at IS NULL AND token_id IS NULL AND credential_expires_at IS NULL)
    OR(status='cancelled' AND decided_by IS NULL AND decided_at IS NOT NULL AND token_id IS NULL AND credential_expires_at IS NULL)
    OR(status='denied' AND decided_by IS NOT NULL AND decided_at IS NOT NULL AND token_id IS NULL AND credential_expires_at IS NULL)
    OR(status='approved' AND decided_by IS NOT NULL AND decided_at IS NOT NULL AND token_id IS NOT NULL AND credential_expires_at IS NOT NULL))
);
CREATE INDEX identity_cli_enrollments_expiry ON identity_cli_enrollments(expires_at);

-- Independent API roots outlive browser logout, but never local OIDC account/binding/configuration revocation.
CREATE TABLE identity_cli_grants(
 token_id uuid PRIMARY KEY REFERENCES api_tokens(id),
 binding_id uuid NOT NULL REFERENCES identity_bindings(id),binding_version bigint NOT NULL,
 configuration text NOT NULL CHECK(configuration ~ '^[0-9a-f]{64}$'),
 policy text NOT NULL CHECK(policy='cli-enrollment-v1'),created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX identity_cli_grants_binding ON identity_cli_grants(binding_id,token_id);
