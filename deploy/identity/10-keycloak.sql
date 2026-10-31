DO $init$
DECLARE password text := regexp_replace(pg_read_file('/run/secrets/app_password'), E'[\r\n]+$', '');
BEGIN
 IF password !~ '^[A-Za-z0-9_-]{43}$' THEN RAISE EXCEPTION 'Invalid identity database password file'; END IF;
 EXECUTE format('CREATE ROLE keycloak LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L', password);
END
$init$;
CREATE DATABASE keycloak OWNER keycloak;
