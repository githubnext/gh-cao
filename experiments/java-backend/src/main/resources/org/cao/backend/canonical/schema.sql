-- Java backend canonical storage (specs/dashboard-data.md §6-9, §36; server-ingestion.md §7). This is
-- NOT the Go server's generated typed schema (server/spec/storage.tsp): rows are kept as JSONB with only
-- the relationship keys typed, so a database provisioned by the Go server and one provisioned here are not
-- interchangeable. Created only by the schema owner through CanonicalBootstrap; runtime never runs DDL.
CREATE TABLE IF NOT EXISTS cao_canonical_state (
    namespace TEXT PRIMARY KEY CHECK (namespace <> ''),
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    data_revision TEXT NOT NULL DEFAULT '',
    evaluated_at TIMESTAMPTZ,
    schema_version INTEGER NOT NULL DEFAULT 1 CHECK (schema_version = 1),
    CHECK ((revision = 0) = (data_revision = ''))
);

CREATE TABLE IF NOT EXISTS cao_canonical_campaigns (
    namespace TEXT NOT NULL REFERENCES cao_canonical_state(namespace) ON DELETE CASCADE,
    id TEXT NOT NULL CHECK (id <> ''),
    ordinal BIGINT NOT NULL CHECK (ordinal >= 0),
    record JSONB NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    PRIMARY KEY (namespace, id),
    UNIQUE (namespace, ordinal)
);

CREATE TABLE IF NOT EXISTS cao_canonical_repositories (
    namespace TEXT NOT NULL REFERENCES cao_canonical_state(namespace) ON DELETE CASCADE,
    id TEXT NOT NULL CHECK (id <> ''),
    ordinal BIGINT NOT NULL CHECK (ordinal >= 0),
    record JSONB NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    PRIMARY KEY (namespace, id),
    UNIQUE (namespace, ordinal)
);

CREATE TABLE IF NOT EXISTS cao_canonical_workflows (
    namespace TEXT NOT NULL REFERENCES cao_canonical_state(namespace) ON DELETE CASCADE,
    id TEXT NOT NULL CHECK (id <> ''),
    ordinal BIGINT NOT NULL CHECK (ordinal >= 0),
    repository_id TEXT NOT NULL CHECK (repository_id <> ''),
    campaign_id TEXT,
    record JSONB NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    PRIMARY KEY (namespace, id),
    UNIQUE (namespace, ordinal),
    FOREIGN KEY (namespace, repository_id) REFERENCES cao_canonical_repositories(namespace, id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (namespace, campaign_id) REFERENCES cao_canonical_campaigns(namespace, id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE IF NOT EXISTS cao_canonical_runs (
    namespace TEXT NOT NULL REFERENCES cao_canonical_state(namespace) ON DELETE CASCADE,
    id TEXT NOT NULL CHECK (id <> ''),
    ordinal BIGINT NOT NULL CHECK (ordinal >= 0),
    repository_id TEXT NOT NULL CHECK (repository_id <> ''),
    workflow_id TEXT NOT NULL CHECK (workflow_id <> ''),
    record JSONB NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    PRIMARY KEY (namespace, id),
    UNIQUE (namespace, ordinal),
    FOREIGN KEY (namespace, repository_id) REFERENCES cao_canonical_repositories(namespace, id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (namespace, workflow_id) REFERENCES cao_canonical_workflows(namespace, id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX IF NOT EXISTS cao_canonical_runs_workflow
    ON cao_canonical_runs(namespace, workflow_id, ordinal);
CREATE INDEX IF NOT EXISTS cao_canonical_runs_repository
    ON cao_canonical_runs(namespace, repository_id, ordinal);
