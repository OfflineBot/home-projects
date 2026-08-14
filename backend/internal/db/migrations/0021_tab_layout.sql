-- A tab is either a grid or a page.
--
-- The grid is right for a wall of numbers and wrong for something that reads
-- like a page: a heading, a paragraph, three shortcuts, another heading. In
-- "flow" the cards simply follow one another and each says how wide it is —
-- no coordinates, nothing to place. The grid stays for whoever wants it.
ALTER TABLE board_tabs ADD COLUMN IF NOT EXISTS layout text NOT NULL DEFAULT 'grid'
    CHECK (layout IN ('grid', 'flow'));
