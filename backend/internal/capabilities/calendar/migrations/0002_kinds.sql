-- The five kinds of entry, and what a deadline needs beyond an appointment.
--
-- Same rule as before: every column here is derived from the file and can be
-- thrown away. A deadline is a VTODO with a DUE, a phase is a VEVENT carrying
-- X-HOME-KIND — the index just saves the month view a directory walk.

ALTER TABLE calendar_events
    ADD COLUMN kind        text        NOT NULL DEFAULT '',
    ADD COLUMN is_todo     boolean     NOT NULL DEFAULT false,
    ADD COLUMN status      text        NOT NULL DEFAULT '',
    ADD COLUMN completed   timestamptz,
    ADD COLUMN priority    integer     NOT NULL DEFAULT 0,
    ADD COLUMN categories  text        NOT NULL DEFAULT '',
    ADD COLUMN related_to  text        NOT NULL DEFAULT '',
    -- My own note on an entry someone else's scheduler owns. It lives in this
    -- project's own file and points at the foreign UID, so a pull can never
    -- delete it.
    ADD COLUMN attached_to text        NOT NULL DEFAULT '',
    ADD COLUMN link        text        NOT NULL DEFAULT '',
    ADD COLUMN person      text        NOT NULL DEFAULT '';

-- The deadline panel and the overdue tile ask this question on every load.
CREATE INDEX calendar_events_kind_idx ON calendar_events (project_id, kind, dtstart);

-- Google Calendar ignores VTODO on a subscribed feed. This switch converts
-- deadlines to short events on the way out — per project, because it is the
-- project that gets subscribed to.
ALTER TABLE calendar_settings
    ADD COLUMN todos_as_events boolean NOT NULL DEFAULT true;
