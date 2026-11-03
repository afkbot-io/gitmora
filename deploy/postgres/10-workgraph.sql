-- The official image executes this only when initializing a new data volume.
-- Credentials are read by PostgreSQL from its explicit mounted file, never command arguments or environment.
DO $init$
DECLARE password text := regexp_replace(pg_read_file('/run/secrets/app_password'), E'[\r\n]+$', '');
BEGIN
 IF password !~ '^[A-Za-z0-9_-]{43}$' THEN RAISE EXCEPTION 'Invalid application password file'; END IF;
 EXECUTE format('CREATE ROLE workgraph LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L', password);
END
$init$;
CREATE DATABASE workgraph OWNER workgraph;
\connect workgraph
CREATE EXTENSION IF NOT EXISTS vector;
