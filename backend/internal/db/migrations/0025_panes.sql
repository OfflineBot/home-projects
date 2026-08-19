-- A tab can be a split surface: panes, the way a terminal multiplexer or an
-- editor does it.
--
-- The grid measures cards in rows of about ninety pixels, which makes a tab a
-- page that happens to be long. That is right for an overview and wrong for
-- something you work in: a terminal beside a machine beside a log, filling the
-- screen exactly, resized by dragging the line between them.
--
-- The tree itself lives in the tab's style — {"panes": {"dir":"row","ratio":0.5,
-- "a":{"card":"…"},"b":{"card":"…"}}} — because it is a description of an
-- arrangement, which is what style already holds. The cards stay ordinary
-- cards; only where they sit is different.
ALTER TABLE board_tabs DROP CONSTRAINT IF EXISTS board_tabs_layout_check;
ALTER TABLE board_tabs ADD CONSTRAINT board_tabs_layout_check
    CHECK (layout IN ('grid', 'flow', 'free', 'page', 'panes'));
