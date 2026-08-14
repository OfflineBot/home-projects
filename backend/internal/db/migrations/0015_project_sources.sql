-- A project that gathers other projects.
--
-- One calendar per thing is right for keeping them apart and wrong for looking
-- at the week: what a person wants in front of them is all of it at once. The
-- same is true of mail, of notes, of anything a view can merge.
--
-- So this is not a "hero calendar" written into the core, which would be one
-- special case pretending to be a feature. It is a list of projects a project
-- gathers, and each capability's view decides what that means for it. A
-- calendar shows their entries beside its own; a view that does not care is
-- unaffected.
CREATE TABLE project_sources (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_id  uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    position   integer NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, source_id),
    CHECK (project_id <> source_id)
);
CREATE INDEX project_sources_source_idx ON project_sources (source_id);
