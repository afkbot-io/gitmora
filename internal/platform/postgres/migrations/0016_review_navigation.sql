ALTER TABLE review_runs DROP CONSTRAINT review_runs_status_check;
ALTER TABLE review_runs ADD CONSTRAINT review_runs_status_check CHECK(status IN('pending','running','succeeded','stale','failed','blocked','cancelled'));
ALTER TABLE review_runs ADD COLUMN cancelled_at timestamptz;
ALTER TABLE review_runs ADD COLUMN cancelled_by uuid REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE review_runs ADD CONSTRAINT review_cancelled_time CHECK((status='cancelled')=(cancelled_at IS NOT NULL));
ALTER TABLE review_runs ADD CONSTRAINT review_cancelled_output CHECK(status<>'cancelled' OR(source IS NULL AND report IS NULL));
CREATE INDEX review_project_identity_idx ON review_runs(project_id,id);
CREATE TABLE review_cancellations(
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 run_id uuid NOT NULL REFERENCES review_runs(id) ON DELETE CASCADE,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 2048),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,request_id)
);
