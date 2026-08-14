-- A project says which filters it uses.
--
-- The other way round — a scheduler naming a filter — put the knowledge in the
-- wrong place: a scheduler only knows how to fetch, and where things belong is
-- the project's business. This way a filter is written once and any project can
-- pick it up, including projects nothing fetches into.
CREATE TABLE project_filters (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    filter_id  uuid NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
    -- Order matters: what an earlier filter takes, a later one does not see.
    position   integer NOT NULL DEFAULT 0,
    -- Whether it runs by itself after something writes into the project.
    automatic  boolean NOT NULL DEFAULT false,
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, filter_id)
);
CREATE INDEX project_filters_filter_idx ON project_filters (filter_id);
