-- Projects can sit in a folder inside their group.
--
-- A group with four projects is a list; a group with twenty is a pile. A folder
-- is one word on a project — no new object, no new page, nothing to keep in
-- sync. Empty means the project sits at the top, where everything was before.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS folder text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS projects_folder_idx ON projects (group_id, folder);
