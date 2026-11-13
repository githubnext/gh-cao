\set ON_ERROR_STOP on
BEGIN;
DO $provision$
DECLARE
  role_name text;
BEGIN
  FOREACH role_name IN ARRAY ARRAY[
    'cao_owner', 'cao_runtime', 'cao_retention', 'cao_evidence',
    'cao_bootstrap_login', 'cao_runtime_login'
  ] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = role_name) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', role_name);
    END IF;
    IF EXISTS (
      SELECT FROM pg_roles WHERE rolname = role_name AND
        (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls
         OR (rolcanlogin AND role_name NOT IN ('cao_bootstrap_login', 'cao_runtime_login')))
    ) THEN
      RAISE EXCEPTION 'Refusing previously privileged role %; use fresh deployment roles', role_name;
    END IF;
  END LOOP;
  IF EXISTS (
    SELECT FROM pg_auth_members m
    JOIN pg_roles member ON member.oid = m.member
    JOIN pg_roles parent ON parent.oid = m.roleid
    WHERE member.rolname IN ('cao_owner', 'cao_runtime', 'cao_retention', 'cao_evidence',
                            'cao_bootstrap_login', 'cao_runtime_login')
      AND (m.admin_option OR NOT
        ((member.rolname = 'cao_bootstrap_login' AND parent.rolname = 'cao_owner')
          OR (member.rolname = 'cao_runtime_login' AND parent.rolname = 'cao_runtime')))
  ) THEN
    RAISE EXCEPTION 'Refusing unexpected inherited role authority';
  END IF;
END
$provision$;
GRANT cao_owner TO CURRENT_USER;
GRANT cao_owner TO cao_bootstrap_login;
GRANT cao_runtime TO cao_runtime_login;
ALTER ROLE cao_bootstrap_login LOGIN INHERIT;
ALTER ROLE cao_runtime_login LOGIN INHERIT;
CREATE SCHEMA IF NOT EXISTS cao AUTHORIZATION cao_owner;
DO $schema$
BEGIN
  IF (SELECT nspowner FROM pg_namespace WHERE nspname = 'cao') <> 'cao_owner'::regrole THEN
    RAISE EXCEPTION 'Existing cao schema is not owned by cao_owner; use a new database';
  END IF;
END
$schema$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA cao FROM PUBLIC;
SELECT format('REVOKE CONNECT, TEMPORARY ON DATABASE %I FROM PUBLIC', current_database()) \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO cao_owner, cao_runtime, cao_retention, cao_evidence', current_database()) \gexec
SELECT format('GRANT TEMPORARY ON DATABASE %I TO cao_owner, cao_runtime', current_database()) \gexec
COMMIT;
