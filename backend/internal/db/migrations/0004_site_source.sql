-- A site does not have to hold its own files.
--
-- Until now, publishing meant "this project serves one of its own folders", so
-- a site and the material it shows had to be the same project. They are not the
-- same thing: the material is a folder that gets written, linked and pulled
-- into; the site is an address, a folder inside that material, and whether a
-- password stands in front of it.
--
-- Empty keeps the old meaning exactly: the project serves itself.
ALTER TABLE projects
    ADD COLUMN site_source_project_id uuid REFERENCES projects(id) ON DELETE SET NULL;
