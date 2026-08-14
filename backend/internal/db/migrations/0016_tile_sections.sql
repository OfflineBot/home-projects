-- Tiles in sections, and who may see each one.
--
-- The dashboard collects a lot once a few projects report things, and a long
-- row of tiles in no order is worse than a short one. A section is a heading a
-- person writes themselves; the tiles under it are theirs to arrange.
--
-- Visibility is the other half. A dashboard is the first page anybody lands on,
-- including someone with no account, and "everything or nothing" is not an
-- answer. A tile says for itself: private, public, or behind the password of
-- what it shows.
ALTER TABLE dashboard_tiles ADD COLUMN IF NOT EXISTS section text NOT NULL DEFAULT '';
ALTER TABLE dashboard_tiles ADD COLUMN IF NOT EXISTS visibility text NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('private', 'public', 'password'));
