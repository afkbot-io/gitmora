CREATE TABLE users (
    id uuid PRIMARY KEY,
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 200),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('reader', 'writer', 'admin')),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships(user_id, project_id);

CREATE TABLE repositories (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    gitlab_url text NOT NULL,
    gitlab_project_id bigint NOT NULL CHECK (gitlab_project_id > 0),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (gitlab_url, gitlab_project_id)
);
CREATE INDEX repositories_project_idx ON repositories(project_id);

CREATE TABLE capture_sessions (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    session_id uuid NOT NULL,
    originator_id uuid NOT NULL REFERENCES users(id),
    identity_digest text NOT NULL CHECK (length(identity_digest) = 64),
    latest_revision bigint NOT NULL CHECK (latest_revision BETWEEN 1 AND 1000000000),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, session_id)
);

CREATE TABLE capture_revisions (
    repository_id uuid NOT NULL,
    session_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 1000000000),
    digest text NOT NULL CHECK (length(digest) = 64),
    canonical bytea NOT NULL CHECK (octet_length(canonical) BETWEEN 1 AND 2097152),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, session_id, revision),
    FOREIGN KEY (repository_id, session_id) REFERENCES capture_sessions(repository_id, session_id) ON DELETE CASCADE
);

CREATE TABLE commits (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    sha text NOT NULL CHECK (sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    confirmed_at timestamptz,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, sha)
);

CREATE TABLE capture_revision_commits (
    repository_id uuid NOT NULL,
    session_id uuid NOT NULL,
    revision bigint NOT NULL,
    sha text NOT NULL,
    PRIMARY KEY (repository_id, session_id, revision, sha),
    FOREIGN KEY (repository_id, session_id, revision) REFERENCES capture_revisions(repository_id, session_id, revision) ON DELETE CASCADE,
    FOREIGN KEY (repository_id, sha) REFERENCES commits(repository_id, sha) ON DELETE CASCADE
);
CREATE INDEX capture_revision_commits_sha_idx ON capture_revision_commits(repository_id, sha);

CREATE TABLE outbox_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    event_key text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    available_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_owner text,
    lease_until timestamptz,
    completed_at timestamptz,
    UNIQUE (repository_id, event_key)
);
CREATE INDEX outbox_pending_idx ON outbox_events(available_at, id) WHERE completed_at IS NULL;
