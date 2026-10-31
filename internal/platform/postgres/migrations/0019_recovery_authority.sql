-- Recovery receipts/authority history grant no runtime permission. Only the current fresh operator is active.
CREATE TABLE recovery_events(
 id uuid PRIMARY KEY,
 backup_id uuid NOT NULL,
 parent_id uuid REFERENCES recovery_events(id),
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 schema_fingerprint text NOT NULL CHECK(schema_fingerprint ~ '^[0-9a-f]{64}$'),
 user_id uuid NOT NULL REFERENCES users(id),
 token_id uuid NOT NULL REFERENCES api_tokens(id),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=4096),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE recovery_state(id integer PRIMARY KEY CHECK(id=1),head_id uuid REFERENCES recovery_events(id));
INSERT INTO recovery_state(id)VALUES(1);
CREATE TABLE recovery_memberships(
 recovery_id uuid NOT NULL REFERENCES recovery_events(id) DEFERRABLE INITIALLY DEFERRED,
 project_id uuid NOT NULL,user_id uuid NOT NULL,
 role text NOT NULL CHECK(role IN('reader','writer','admin')),version bigint NOT NULL,
 PRIMARY KEY(recovery_id,project_id,user_id)
);
CREATE TABLE recovery_operators(
 recovery_id uuid NOT NULL REFERENCES recovery_events(id) DEFERRABLE INITIALLY DEFERRED,
 user_id uuid NOT NULL,PRIMARY KEY(recovery_id,user_id)
);
