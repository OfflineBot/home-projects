-- Backlink index for markdown projects. Derived from the notes themselves and
-- rebuildable at any time.

CREATE TABLE markdown_links (
    id          bigserial PRIMARY KEY,
    project_id  uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_path text NOT NULL,
    target      text NOT NULL
);
CREATE INDEX markdown_links_target_idx ON markdown_links (project_id, lower(target));
CREATE INDEX markdown_links_source_idx ON markdown_links (project_id, source_path);
