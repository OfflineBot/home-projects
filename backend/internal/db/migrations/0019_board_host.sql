-- A group's board can have an address of its own: dhbw.example.com shows that
-- board and nothing else — no navigation, no editing, and only the cards that
-- are public. It is the same board, seen by somebody who was handed the link.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS board_host text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS groups_board_host_idx ON groups (lower(board_host))
    WHERE board_host <> '';
