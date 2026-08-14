-- A scheduler may run its results through several filters, in order.
--
-- One filter per scheduler was one too few the moment the material had more
-- than one shape: a Moodle pull wants a filter for the first semester, one for
-- the second, one for the seminars. Rules are tried top to bottom and the first
-- match takes the file, so putting the filters in a row is the same thing as
-- putting their rules in a row.
CREATE TABLE scheduler_filters (
    scheduler_id uuid NOT NULL REFERENCES schedulers(id) ON DELETE CASCADE,
    filter_id    uuid NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
    position     integer NOT NULL DEFAULT 0,
    PRIMARY KEY (scheduler_id, filter_id)
);
CREATE INDEX scheduler_filters_filter_idx ON scheduler_filters (filter_id);

-- What was already set stays set.
INSERT INTO scheduler_filters (scheduler_id, filter_id, position)
SELECT id, filter_id, 0 FROM schedulers WHERE filter_id IS NOT NULL;

ALTER TABLE schedulers DROP COLUMN filter_id;
