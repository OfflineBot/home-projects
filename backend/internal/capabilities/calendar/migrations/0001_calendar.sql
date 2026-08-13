-- The index over the .ics files of calendar projects.
--
-- The file stays the truth: every row here is derived from a VEVENT and can be
-- thrown away and rebuilt at any time. It exists so a month view and a group
-- overlay are one query instead of a directory walk.

CREATE TABLE calendar_events (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    uid           text NOT NULL,
    -- Set for a single changed appearance out of a series (RECURRENCE-ID).
    recurrence_id timestamptz,
    dtstart       timestamptz NOT NULL,
    dtend         timestamptz NOT NULL,
    all_day       boolean NOT NULL DEFAULT false,
    summary       text NOT NULL DEFAULT '',
    description   text NOT NULL DEFAULT '',
    location      text NOT NULL DEFAULT '',
    rrule         text NOT NULL DEFAULT '',
    exdates       text NOT NULL DEFAULT '',
    color         text NOT NULL DEFAULT '',
    alarms        text NOT NULL DEFAULT '',
    sequence      integer NOT NULL DEFAULT 0,
    -- Which file the event came from, and whether it may be edited. Events
    -- pulled from a subscription are overwritten on the next run.
    source_file   text NOT NULL DEFAULT 'calendar.ics',
    read_only     boolean NOT NULL DEFAULT false,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX calendar_events_project_idx ON calendar_events (project_id, dtstart);
CREATE INDEX calendar_events_uid_idx ON calendar_events (project_id, uid);

-- Per-project calendar settings. Only two so far: whether events are kept in
-- one file or one file each, and which view was last used.
CREATE TABLE calendar_settings (
    project_id uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    split      boolean NOT NULL DEFAULT false,
    last_view  text NOT NULL DEFAULT 'month'
);
