-- A tab can be one page, written by hand.
--
-- Cards are right when a board is made of parts. They are in the way when
-- somebody wants to write the page itself — or have an assistant write it. Such
-- a tab holds one HTML document and nothing else, and there is one address to
-- read it and one to replace it.
ALTER TABLE board_tabs DROP CONSTRAINT IF EXISTS board_tabs_layout_check;
ALTER TABLE board_tabs ADD CONSTRAINT board_tabs_layout_check
    CHECK (layout IN ('grid', 'flow', 'free', 'page'));
