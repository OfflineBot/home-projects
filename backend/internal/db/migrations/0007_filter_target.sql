-- Where a filter sends things is set where it is picked up, not where it is
-- written. A filter is then a pattern — "folders called Grundlagen-something" —
-- and the same one serves two projects that want different destinations.
--
-- A rule that names a project itself still wins; this is the answer for the
-- rules that do not.
ALTER TABLE project_filters
    ADD COLUMN target_project_id uuid REFERENCES projects(id) ON DELETE SET NULL,
    ADD COLUMN target_folder text NOT NULL DEFAULT '';
