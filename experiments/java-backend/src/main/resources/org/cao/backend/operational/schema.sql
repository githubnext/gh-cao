-- Operational audit subset of server/internal/operational/postgres/schema.sql (the Go adapter owns
-- the full contract). Run only by the schema owner through OperationalBootstrap; runtime never
-- executes DDL. {{schema}} is replaced by the owner's current schema.
CREATE TABLE IF NOT EXISTS cao_operational_namespaces (
    namespace TEXT PRIMARY KEY,
    version INTEGER NOT NULL DEFAULT 2 CHECK (version = 2),
    config BYTEA NOT NULL,
    protected_bytes BIGINT NOT NULL DEFAULT 0 CHECK (protected_bytes >= 0),
    protected_entries BIGINT NOT NULL DEFAULT 0 CHECK (protected_entries >= 0)
);

CREATE TABLE IF NOT EXISTS cao_operational_audit_events (
    namespace TEXT NOT NULL REFERENCES cao_operational_namespaces(namespace) ON DELETE CASCADE,
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    occurred_at TIMESTAMPTZ NOT NULL,
    event TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('success','denied','failure')),
    actor TEXT NOT NULL DEFAULT '',
    record TEXT NOT NULL,
    prev_hash BYTEA NOT NULL CHECK (octet_length(prev_hash) = 32),
    hash BYTEA NOT NULL CHECK (octet_length(hash) = 32),
    PRIMARY KEY (namespace, sequence)
);
CREATE INDEX IF NOT EXISTS cao_operational_audit_time
    ON cao_operational_audit_events(namespace, occurred_at);
CREATE INDEX IF NOT EXISTS cao_operational_audit_actor
    ON cao_operational_audit_events(namespace, actor, sequence);

CREATE OR REPLACE FUNCTION cao_operational_audit_append_only() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF TG_OP = 'DELETE'
        AND current_user = pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = TG_RELID))
        AND (pg_trigger_depth() > 1 OR current_setting('cao.audit_retention', true) = 'on') THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'cao_operational_audit_events is append-only' USING ERRCODE = 'insufficient_privilege';
END;
$$;
DROP TRIGGER IF EXISTS cao_operational_audit_append_only ON cao_operational_audit_events;
CREATE TRIGGER cao_operational_audit_append_only
    BEFORE UPDATE OR DELETE ON cao_operational_audit_events
    FOR EACH ROW EXECUTE FUNCTION cao_operational_audit_append_only();

CREATE OR REPLACE FUNCTION cao_operational_namespace_owner_only() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF current_user <> pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = TG_RELID)) THEN
        RAISE EXCEPTION 'namespace destruction requires the schema owner' USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS cao_operational_namespace_owner_only ON cao_operational_namespaces;
CREATE TRIGGER cao_operational_namespace_owner_only
    BEFORE DELETE ON cao_operational_namespaces
    FOR EACH ROW EXECUTE FUNCTION cao_operational_namespace_owner_only();

CREATE OR REPLACE FUNCTION cao_operational_retain_audit(target_namespace TEXT) RETURNS BIGINT
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    removed BIGINT;
    cutoff TIMESTAMPTZ;
BEGIN
    PERFORM namespace FROM {{schema}}.cao_operational_namespaces WHERE namespace = target_namespace FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'operational namespace is not provisioned';
    END IF;
    cutoff := clock_timestamp() - INTERVAL '9600 hours';
    PERFORM set_config('cao.audit_retention', 'on', true);
    DELETE FROM {{schema}}.cao_operational_audit_events WHERE namespace = target_namespace AND sequence IN (
        SELECT sequence FROM {{schema}}.cao_operational_audit_events WHERE namespace = target_namespace
        AND sequence < LEAST(
            coalesce((SELECT min(sequence) FROM {{schema}}.cao_operational_audit_events
                WHERE namespace = target_namespace AND occurred_at >= cutoff), 9223372036854775807),
            (SELECT max(sequence) FROM {{schema}}.cao_operational_audit_events WHERE namespace = target_namespace))
        ORDER BY sequence LIMIT 512);
    GET DIAGNOSTICS removed = ROW_COUNT;
    PERFORM set_config('cao.audit_retention', 'off', true);
    RETURN removed;
END;
$$;
REVOKE ALL ON FUNCTION cao_operational_retain_audit(TEXT) FROM PUBLIC;
