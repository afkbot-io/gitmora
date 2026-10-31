CREATE TABLE capture_context_scopes(
 repository_id uuid NOT NULL,session_id uuid NOT NULL,
 started_at_text text NOT NULL,agent_name text NOT NULL,
 country_id uuid,task_id uuid,
 PRIMARY KEY(repository_id,session_id),
 FOREIGN KEY(repository_id,session_id) REFERENCES capture_sessions(repository_id,session_id) ON DELETE CASCADE,
 CHECK((country_id IS NULL)=(task_id IS NULL))
);
CREATE INDEX capture_context_task ON capture_context_scopes(country_id,task_id,repository_id);
INSERT INTO capture_context_scopes(repository_id,session_id,started_at_text,agent_name,country_id,task_id)
 SELECT s.repository_id,s.session_id,convert_from(r.canonical,'UTF8')::jsonb->>'started_at',
 convert_from(r.canonical,'UTF8')::jsonb->'agent'->>'name',
 (convert_from(r.canonical,'UTF8')::jsonb->'task'->>'country_id')::uuid,
 (convert_from(r.canonical,'UTF8')::jsonb->'task'->>'task_id')::uuid
 FROM capture_sessions s JOIN capture_revisions r ON r.repository_id=s.repository_id AND r.session_id=s.session_id AND r.revision=s.latest_revision;
