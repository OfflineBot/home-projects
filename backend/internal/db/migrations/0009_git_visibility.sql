-- Whether the repository is as open as the group, or not.
--
-- They are two different questions. A group can be public — its projects
-- listed, its site served — while `git clone` still asks for something; and a
-- password-protected group can hand its repository out freely. Empty means the
-- old answer: the repository is as open as the group.
ALTER TABLE groups ADD COLUMN git_visibility text NOT NULL DEFAULT '';
