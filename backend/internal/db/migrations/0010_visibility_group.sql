-- A project may defer to its group.
--
-- The constraint listed the three answers a project could give; there is a
-- fourth now, and it is the default for a new one: whatever the group says. A
-- group that opens later then takes its projects with it, which copying the
-- answer once would not.
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_visibility_check;
ALTER TABLE projects ADD CONSTRAINT projects_visibility_check
    CHECK (visibility IN ('group', 'private', 'public', 'password'));
