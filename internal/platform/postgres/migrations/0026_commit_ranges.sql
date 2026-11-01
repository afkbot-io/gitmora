CREATE TABLE capture_commit_ranges (
 repository_id uuid NOT NULL, session_id uuid NOT NULL, revision bigint NOT NULL,
 sha text NOT NULL CHECK(sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
 start_message integer NOT NULL CHECK(start_message>=0),end_message integer NOT NULL CHECK(end_message>start_message),
 basis text NOT NULL CHECK(basis IN('observed_checkpoint','recovered_git_output','carry_forward')),
 PRIMARY KEY(repository_id,session_id,revision,sha,start_message,end_message),
 FOREIGN KEY(repository_id,session_id,revision) REFERENCES capture_revisions(repository_id,session_id,revision) ON DELETE CASCADE
);
CREATE INDEX capture_commit_ranges_sha ON capture_commit_ranges(repository_id,sha,session_id,revision);
