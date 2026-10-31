CREATE TABLE api_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 120),
    secret_hash text NOT NULL UNIQUE CHECK (secret_hash ~ '^[0-9a-f]{64}$'),
    scopes text[] NOT NULL CHECK (
        cardinality(scopes) BETWEEN 1 AND 2
        AND scopes <@ ARRAY['capture:read', 'capture:write']::text[]
    ),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    revoked_at timestamptz
);
CREATE INDEX api_tokens_user_idx ON api_tokens(user_id);
