CREATE TABLE capture_continuations(
 repository_id uuid NOT NULL,session_id uuid NOT NULL,root_session_id uuid NOT NULL,previous_session_id uuid NOT NULL,
 previous_revision bigint NOT NULL CHECK(previous_revision BETWEEN 1 AND 1000000000),
 previous_digest text NOT NULL CHECK(previous_digest ~ '^[0-9a-f]{64}$'),segment bigint NOT NULL CHECK(segment BETWEEN 2 AND 1000000000),reason text NOT NULL,
 PRIMARY KEY(repository_id,session_id),FOREIGN KEY(repository_id,session_id) REFERENCES capture_sessions(repository_id,session_id) ON DELETE CASCADE,
 CHECK(session_id<>previous_session_id AND session_id<>root_session_id),CHECK(reason IN('window','compaction','history_replacement','protocol_upgrade'))
);
CREATE INDEX capture_continuations_root ON capture_continuations(repository_id,root_session_id,segment,session_id);
