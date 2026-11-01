CREATE TABLE user_handles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    username text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9._-]{0,79}$')
);

-- A connection belongs to a local project. Reusing an upstream repository in
-- another company/project must neither conflict nor share captured evidence.
ALTER TABLE repositories DROP CONSTRAINT repositories_gitlab_url_gitlab_project_id_key;
CREATE UNIQUE INDEX repositories_project_forge_identity ON repositories (
    project_id, provider,
    (CASE WHEN provider='github' THEN regexp_replace(gitlab_url,'/[^/]+/[^/]+$','') ELSE gitlab_url END),
    gitlab_project_id
);
