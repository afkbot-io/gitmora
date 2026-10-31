ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check CHECK(cardinality(scopes) BETWEEN 1 AND 13 AND scopes <@ ARRAY['capture:read','capture:write','integration:read','integration:manage','knowledge:read','knowledge:write','knowledge:review','review:read','review:run','directory:read','directory:manage','platform:manage','credentials:manage']::text[]);

CREATE SEQUENCE directory_revision AS bigint MINVALUE 1 MAXVALUE 9007199254740991 NO CYCLE;
ALTER TABLE users ADD COLUMN version bigint NOT NULL DEFAULT nextval('directory_revision') CHECK(version BETWEEN 1 AND 9007199254740991);
ALTER TABLE projects ADD COLUMN version bigint NOT NULL DEFAULT nextval('directory_revision') CHECK(version BETWEEN 1 AND 9007199254740991);
ALTER TABLE repositories ADD COLUMN version bigint NOT NULL DEFAULT nextval('directory_revision') CHECK(version BETWEEN 1 AND 9007199254740991);
ALTER TABLE memberships ADD COLUMN version bigint NOT NULL DEFAULT nextval('directory_revision') CHECK(version BETWEEN 1 AND 9007199254740991);
ALTER TABLE users ADD COLUMN deleted_at timestamptz;
ALTER TABLE users ADD CONSTRAINT users_deleted_disabled CHECK(deleted_at IS NULL OR NOT enabled);

CREATE TABLE directory_operators(user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE);
-- Existing project administrators are deliberately not promoted during upgrade.
CREATE TABLE directory_identifiers(
 kind text NOT NULL CHECK(kind IN('user','project','repository')),
 id uuid NOT NULL,retired_at timestamptz,PRIMARY KEY(kind,id)
);
INSERT INTO directory_identifiers(kind,id) SELECT 'user',id FROM users UNION ALL SELECT 'project',id FROM projects UNION ALL SELECT 'repository',id FROM repositories;
CREATE TABLE directory_commands(
 actor_id uuid NOT NULL REFERENCES users(id),request_id uuid NOT NULL,
 fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=4096),
 PRIMARY KEY(actor_id,request_id)
);
CREATE TABLE directory_events(
 seq bigint PRIMARY KEY DEFAULT nextval('directory_revision'),
 actor_id uuid NOT NULL REFERENCES users(id),token_id uuid,
 project_id uuid,entity_id uuid NOT NULL,
 origin text NOT NULL CHECK(origin IN('api','offline')),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=4096),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX directory_events_project ON directory_events(project_id,seq);
ALTER TABLE api_tokens ADD COLUMN parent_id uuid REFERENCES api_tokens(id) ON DELETE CASCADE;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_not_own_parent CHECK(parent_id IS NULL OR parent_id<>id);
CREATE INDEX api_tokens_parent ON api_tokens(parent_id) WHERE parent_id IS NOT NULL;
