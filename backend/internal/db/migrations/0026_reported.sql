-- Which numbers are the point, and which are the bookkeeping.
--
-- A project reports both: the average of the marks, and how many automation
-- runs failed this week. The first is why the project exists; the second is
-- the system talking about itself. A list that mixes them buries the six
-- things somebody wanted under forty they never asked for.
ALTER TABLE variables ADD COLUMN IF NOT EXISTS reported boolean NOT NULL DEFAULT false;
