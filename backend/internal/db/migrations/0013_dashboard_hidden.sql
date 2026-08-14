-- Taking one thing off the dashboard, rather than a whole group.
--
-- A group brings everything its projects report, which is right the first time
-- and wrong by the third week: one project is finished, one number was only
-- ever interesting once. What is put away is put away per person, and it can
-- be brought back — this is a view, not a deletion.
CREATE TABLE dashboard_hidden (
    owner_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('project','variable')),
    ref        text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, kind, ref)
);
