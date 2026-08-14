-- A tile can be a project itself, not only a number a project reported.
--
-- The dashboard is the page that gets opened every day, so what belongs on it
-- is whatever someone wants at hand: the average out of Dualis, how much mail
-- came in — and the two or three projects they actually work in.
ALTER TABLE dashboard_tiles ALTER COLUMN group_id DROP NOT NULL;
ALTER TABLE dashboard_tiles ADD COLUMN IF NOT EXISTS project_id uuid
    REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE dashboard_tiles DROP CONSTRAINT IF EXISTS dashboard_tiles_kind_check;
ALTER TABLE dashboard_tiles ADD CONSTRAINT dashboard_tiles_kind_check
    CHECK (kind IN ('number','text','status','list','table','history','button','project'));
-- A tile points at one of the two, and says which by its kind.
ALTER TABLE dashboard_tiles ADD CONSTRAINT dashboard_tiles_target_check
    CHECK ((kind = 'project' AND project_id IS NOT NULL) OR (kind <> 'project' AND group_id IS NOT NULL));
