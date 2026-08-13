-- The run log of the automation capability. Failed runs are visible, not
-- silent — this table is what the project's "Runs" panel reads.

CREATE TABLE automation_runs (
    id          bigserial PRIMARY KEY,
    project_id  uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    rule        text NOT NULL,
    trigger     text NOT NULL DEFAULT 'manual',
    status      text NOT NULL DEFAULT 'running',
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    log         text NOT NULL DEFAULT ''
);
CREATE INDEX automation_runs_lookup_idx ON automation_runs (project_id, started_at DESC);
