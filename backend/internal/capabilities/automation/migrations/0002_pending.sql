-- Something to happen later.
--
-- "Switch everything on in five minutes" is not a schedule — it happens once,
-- it is asked for by hand, and until it happens it has to be visible and it has
-- to be possible to call it off. A cron line can do none of those things.
CREATE TABLE IF NOT EXISTS automation_pending (
    id          bigserial PRIMARY KEY,
    project_id  uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    rule        text NOT NULL,
    due_at      timestamptz NOT NULL,
    note        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS automation_pending_due ON automation_pending (due_at);
