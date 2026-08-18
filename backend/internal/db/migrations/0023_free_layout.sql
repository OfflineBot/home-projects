-- A tab can also be a free surface.
--
-- The grid is right for a wall of numbers and the flow for something that reads
-- like a document. Neither is right for "I want to build a page": for that the
-- cards go where they are put, in pixels, and nothing snaps them anywhere.
ALTER TABLE board_tabs DROP CONSTRAINT IF EXISTS board_tabs_layout_check;
ALTER TABLE board_tabs ADD CONSTRAINT board_tabs_layout_check
    CHECK (layout IN ('grid', 'flow', 'free'));
