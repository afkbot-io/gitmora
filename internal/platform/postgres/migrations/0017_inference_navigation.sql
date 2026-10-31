ALTER TABLE inference_runs DROP CONSTRAINT inference_runs_status_check;
ALTER TABLE inference_runs ADD CONSTRAINT inference_runs_status_check CHECK(status IN('pending','running','succeeded','failed','blocked','cancelled'));
ALTER TABLE inference_runs ADD COLUMN cancelled_at timestamptz;
ALTER TABLE inference_runs ADD CONSTRAINT inference_cancelled_time CHECK((status='cancelled')=(cancelled_at IS NOT NULL));
CREATE INDEX inference_owner_history_idx ON inference_runs(user_id,project_id,created_at DESC,id DESC);
CREATE TABLE inference_cancellations(
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 run_id uuid NOT NULL REFERENCES inference_runs(id) ON DELETE CASCADE,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 2048),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,request_id)
);
