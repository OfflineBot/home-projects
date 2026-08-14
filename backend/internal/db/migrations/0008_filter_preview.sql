-- Which projects a filter is tried against while it is being written.
--
-- Only that: the filter stays standalone and knows nothing about them when it
-- runs. It is the difference between typing a folder name from memory and
-- seeing the folders that are actually there.
ALTER TABLE filters ADD COLUMN preview_projects text NOT NULL DEFAULT '';
