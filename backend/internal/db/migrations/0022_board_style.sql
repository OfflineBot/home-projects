-- A board you can design, within limits that keep it a page rather than a mess.
--
-- Not free CSS: a small set of choices — a colour from the palette, a plain or
-- tinted or bare background, a border, bigger text, centred — which is enough
-- to make a board look like a page somebody built and not enough to make it
-- unreadable. It travels in the export like everything else.
ALTER TABLE board_cards ADD COLUMN IF NOT EXISTS style jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE board_tabs  ADD COLUMN IF NOT EXISTS style jsonb NOT NULL DEFAULT '{}'::jsonb;
