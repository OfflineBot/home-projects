-- Boards: a page somebody arranges themselves.
--
-- The dashboard was a list of tiles in a fixed order, and a group's page was a
-- list of projects. Both are the same thing seen twice: a place where somebody
-- wants a few things at hand, arranged the way they think about them. So there
-- is one thing now — a board — and it can sit in three places: the front page,
-- a group's page, and later a project's.
--
-- A board has tabs, a tab has cards, a card sits on a twelve-column grid. What
-- a card *is* comes from the same registry as everything else: the core knows a
-- handful (text, link, a number, a project), and every capability may offer its
-- own. That is the whole reason this is not "the dashboard with more tile
-- kinds": adding a capability adds cards without this file changing.
CREATE TABLE boards (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      text NOT NULL CHECK (scope IN ('home', 'group', 'project')),
    group_id   uuid REFERENCES groups(id) ON DELETE CASCADE,
    project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- One board per place per person.
    CHECK ((scope = 'home'    AND group_id IS NULL AND project_id IS NULL)
        OR (scope = 'group'   AND group_id IS NOT NULL)
        OR (scope = 'project' AND project_id IS NOT NULL))
);
CREATE UNIQUE INDEX boards_home_idx    ON boards (owner_id) WHERE scope = 'home';
CREATE UNIQUE INDEX boards_group_idx   ON boards (owner_id, group_id) WHERE scope = 'group';
CREATE UNIQUE INDEX boards_project_idx ON boards (owner_id, project_id) WHERE scope = 'project';

CREATE TABLE board_tabs (
    id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id uuid NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    title    text NOT NULL DEFAULT 'Board',
    icon     text NOT NULL DEFAULT 'grid',
    position integer NOT NULL DEFAULT 0
);
CREATE INDEX board_tabs_board_idx ON board_tabs (board_id, position);

CREATE TABLE board_cards (
    id     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tab_id uuid NOT NULL REFERENCES board_tabs(id) ON DELETE CASCADE,
    -- kind is a name from the card registry: a core one, or a capability's.
    kind    text NOT NULL,
    options jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Who may see this card, never wider than what it shows.
    visibility text NOT NULL DEFAULT 'private'
               CHECK (visibility IN ('private', 'public', 'password')),
    x integer NOT NULL DEFAULT 0,
    y integer NOT NULL DEFAULT 0,
    w integer NOT NULL DEFAULT 3,
    h integer NOT NULL DEFAULT 2
);
CREATE INDEX board_cards_tab_idx ON board_cards (tab_id, y, x);

-- What was on the dashboard stays on it: every tile becomes a card on the
-- owner's home board, in the order it was in, three columns wide.
INSERT INTO boards (owner_id, scope)
SELECT DISTINCT owner_id, 'home' FROM dashboard_tiles
ON CONFLICT DO NOTHING;

INSERT INTO board_tabs (board_id, title, icon, position)
SELECT id, 'Board', 'grid', 0 FROM boards WHERE scope = 'home';

INSERT INTO board_cards (tab_id, kind, options, visibility, x, y, w, h)
SELECT t.id,
       CASE WHEN d.kind = 'project' THEN 'project' ELSE d.kind END,
       jsonb_strip_nulls(jsonb_build_object(
           'groupId',   nullif(d.group_id::text, ''),
           'projectId', nullif(d.project_id::text, ''),
           'variable',  nullif(d.variable, ''),
           'title',     nullif(d.title, ''),
           'section',   nullif(d.section, '')
       )),
       d.visibility,
       (row_number() OVER (PARTITION BY d.owner_id ORDER BY d.section, d.y, d.x) - 1) % 4 * 3,
       ((row_number() OVER (PARTITION BY d.owner_id ORDER BY d.section, d.y, d.x) - 1) / 4) * 2,
       3, 2
FROM dashboard_tiles d
JOIN boards b ON b.owner_id = d.owner_id AND b.scope = 'home'
JOIN board_tabs t ON t.board_id = b.id;

DROP TABLE dashboard_tiles;
