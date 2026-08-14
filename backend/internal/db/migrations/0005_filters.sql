-- A filter is a named set of rules that answers "where does this belong?".
--
-- It sits next to accounts and schedulers rather than inside one, because the
-- same three lines that split a Moodle pull across semester projects will sort
-- a folder of files. What asks the question is not the filter's business.
CREATE TABLE filters (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug        text NOT NULL UNIQUE,
    title       text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- The ordered rules. First match wins, so the order is the whole logic.
    rules       jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Which filter a scheduler runs its results through. Null is the normal case:
-- everything lands where the scheduler itself points.
ALTER TABLE schedulers ADD COLUMN filter_id uuid REFERENCES filters(id) ON DELETE SET NULL;
