package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// Boards, their tabs and their cards.
//
// A board is read whole: three small queries and the thing is assembled, rather
// than a query per card. There are never enough cards on one page for that to
// be the wrong trade.

func (s *Store) BoardFor(ctx context.Context, ownerID uuid.UUID, scope string,
	groupID, projectID *uuid.UUID) (*model.Board, error) {

	var board model.Board
	err := s.pool.QueryRow(ctx, `
		SELECT id, owner_id, scope, group_id, project_id FROM boards
		WHERE owner_id=$1 AND scope=$2
		  AND group_id IS NOT DISTINCT FROM $3
		  AND project_id IS NOT DISTINCT FROM $4`,
		ownerID, scope, groupID, projectID).
		Scan(&board.ID, &board.OwnerID, &board.Scope, &board.GroupID, &board.ProjectID)
	if err != nil {
		return nil, norm(err)
	}
	if err := s.fillBoard(ctx, &board); err != nil {
		return nil, err
	}
	return &board, nil
}

// EnsureBoard hands back the board for a place, making it — with its first tab —
// the first time somebody looks. A board nobody has arranged yet is an empty
// board, not an error.
func (s *Store) EnsureBoard(ctx context.Context, ownerID uuid.UUID, scope string,
	groupID, projectID *uuid.UUID) (*model.Board, error) {

	board, err := s.BoardFor(ctx, ownerID, scope, groupID, projectID)
	if err == nil {
		return board, nil
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO boards (owner_id, scope, group_id, project_id)
		VALUES ($1,$2,$3,$4) RETURNING id`, ownerID, scope, groupID, projectID).Scan(&id); err != nil {
		return nil, norm(err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO board_tabs (board_id, title, icon, position) VALUES ($1,'Board','grid',0)`,
		id); err != nil {
		return nil, norm(err)
	}
	return s.BoardFor(ctx, ownerID, scope, groupID, projectID)
}

func (s *Store) BoardByID(ctx context.Context, id uuid.UUID) (*model.Board, error) {
	var board model.Board
	err := s.pool.QueryRow(ctx,
		`SELECT id, owner_id, scope, group_id, project_id FROM boards WHERE id=$1`, id).
		Scan(&board.ID, &board.OwnerID, &board.Scope, &board.GroupID, &board.ProjectID)
	if err != nil {
		return nil, norm(err)
	}
	if err := s.fillBoard(ctx, &board); err != nil {
		return nil, err
	}
	return &board, nil
}

func (s *Store) fillBoard(ctx context.Context, board *model.Board) error {
	rows, err := s.pool.Query(ctx,
		`SELECT id, title, icon, layout, style, position FROM board_tabs WHERE board_id=$1 ORDER BY position, title`,
		board.ID)
	if err != nil {
		return norm(err)
	}
	defer rows.Close()
	board.Tabs = []model.BoardTab{}
	for rows.Next() {
		var t model.BoardTab
		if err := rows.Scan(&t.ID, &t.Title, &t.Icon, &t.Layout, &t.Style, &t.Position); err != nil {
			return norm(err)
		}
		t.Cards = []model.BoardCard{}
		board.Tabs = append(board.Tabs, t)
	}
	if err := rows.Err(); err != nil {
		return norm(err)
	}

	cards, err := s.pool.Query(ctx, `
		SELECT c.id, c.tab_id, c.kind, c.options, c.style, c.visibility, c.x, c.y, c.w, c.h
		FROM board_cards c JOIN board_tabs t ON t.id = c.tab_id
		WHERE t.board_id=$1 ORDER BY c.y, c.x`, board.ID)
	if err != nil {
		return norm(err)
	}
	defer cards.Close()
	byTab := map[uuid.UUID][]model.BoardCard{}
	for cards.Next() {
		var c model.BoardCard
		if err := cards.Scan(&c.ID, &c.TabID, &c.Kind, &c.Options, &c.Style, &c.Visibility,
			&c.X, &c.Y, &c.W, &c.H); err != nil {
			return norm(err)
		}
		byTab[c.TabID] = append(byTab[c.TabID], c)
	}
	if err := cards.Err(); err != nil {
		return norm(err)
	}
	for i := range board.Tabs {
		if list, ok := byTab[board.Tabs[i].ID]; ok {
			board.Tabs[i].Cards = list
		}
	}
	return nil
}

// ------------------------------------------------------------------- tabs

func (s *Store) CreateTab(ctx context.Context, boardID uuid.UUID, title, icon string) (*model.BoardTab, error) {
	var t model.BoardTab
	err := s.pool.QueryRow(ctx, `
		INSERT INTO board_tabs (board_id, title, icon, position)
		VALUES ($1,$2,$3, COALESCE((SELECT max(position)+1 FROM board_tabs WHERE board_id=$1),0))
		RETURNING id, title, icon, layout, style, position`, boardID, title, icon).
		Scan(&t.ID, &t.Title, &t.Icon, &t.Layout, &t.Style, &t.Position)
	if err != nil {
		return nil, norm(err)
	}
	t.Cards = []model.BoardCard{}
	return &t, nil
}

type TabPatch struct {
	Title    *string
	Icon     *string
	Layout   *string
	Style    *json.RawMessage
	Position *int
}

func (s *Store) UpdateTab(ctx context.Context, id uuid.UUID, p TabPatch) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE board_tabs SET
			title    = COALESCE($2, title),
			icon     = COALESCE($3, icon),
			layout   = COALESCE($4, layout),
			style    = COALESCE($5, style),
			position = COALESCE($6, position)
		WHERE id=$1`, id, p.Title, p.Icon, p.Layout, p.Style, p.Position)
	return norm(err)
}

func (s *Store) DeleteTab(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM board_tabs WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TabBoard says which board a tab belongs to, so a request about a tab can be
// checked against its owner.
func (s *Store) TabBoard(ctx context.Context, tabID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var boardID, ownerID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT b.id, b.owner_id FROM board_tabs t JOIN boards b ON b.id = t.board_id
		WHERE t.id=$1`, tabID).Scan(&boardID, &ownerID)
	return boardID, ownerID, norm(err)
}

// CardBoard is the same question for a card.
func (s *Store) CardBoard(ctx context.Context, cardID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var boardID, ownerID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT b.id, b.owner_id FROM board_cards c
		JOIN board_tabs t ON t.id = c.tab_id
		JOIN boards b ON b.id = t.board_id
		WHERE c.id=$1`, cardID).Scan(&boardID, &ownerID)
	return boardID, ownerID, norm(err)
}

// ------------------------------------------------------------------- cards

func (s *Store) CreateCard(ctx context.Context, c model.BoardCard) (*model.BoardCard, error) {
	if len(c.Options) == 0 {
		c.Options = json.RawMessage(`{}`)
	}
	if c.Visibility == "" {
		c.Visibility = "private"
	}
	if len(c.Style) == 0 {
		c.Style = json.RawMessage(`{}`)
	}
	if c.W <= 0 {
		c.W = 3
	}
	if c.H <= 0 {
		c.H = 2
	}
	var out model.BoardCard
	err := s.pool.QueryRow(ctx, `
		INSERT INTO board_cards (tab_id, kind, options, style, visibility, x, y, w, h)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, tab_id, kind, options, style, visibility, x, y, w, h`,
		c.TabID, c.Kind, c.Options, c.Style, c.Visibility, c.X, c.Y, c.W, c.H).
		Scan(&out.ID, &out.TabID, &out.Kind, &out.Options, &out.Style, &out.Visibility,
			&out.X, &out.Y, &out.W, &out.H)
	if err != nil {
		return nil, norm(err)
	}
	return &out, nil
}

type CardPatch struct {
	Kind       *string
	Options    *json.RawMessage
	Style      *json.RawMessage
	Visibility *string
	TabID      *uuid.UUID
	X, Y, W, H *int
}

func (s *Store) UpdateCard(ctx context.Context, id uuid.UUID, p CardPatch) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE board_cards SET
			kind       = COALESCE($2, kind),
			options    = COALESCE($3, options),
			style      = COALESCE($4, style),
			visibility = COALESCE($5, visibility),
			tab_id     = COALESCE($6, tab_id),
			x = COALESCE($7, x), y = COALESCE($8, y),
			w = COALESCE($9, w), h = COALESCE($10, h)
		WHERE id=$1`, id, p.Kind, p.Options, p.Style, p.Visibility, p.TabID, p.X, p.Y, p.W, p.H)
	return norm(err)
}

func (s *Store) DeleteCard(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM board_cards WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Layout is what a drag leaves behind: the whole arrangement of a tab in one
// go, rather than one request per card that moved out of the way.
type Layout struct {
	ID    uuid.UUID  `json:"id"`
	TabID *uuid.UUID `json:"tabId,omitempty"`
	X     int        `json:"x"`
	Y     int        `json:"y"`
	W     int        `json:"w"`
	H     int        `json:"h"`
}

func (s *Store) SaveLayout(ctx context.Context, boardID uuid.UUID, cards []Layout) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return norm(err)
	}
	defer tx.Rollback(ctx)
	for _, c := range cards {
		if _, err := tx.Exec(ctx, `
			UPDATE board_cards SET x=$2, y=$3, w=$4, h=$5,
				tab_id = COALESCE($6, tab_id)
			WHERE id=$1 AND tab_id IN (SELECT id FROM board_tabs WHERE board_id=$7)`,
			c.ID, c.X, c.Y, c.W, c.H, c.TabID, boardID); err != nil {
			return norm(err)
		}
	}
	return norm(tx.Commit(ctx))
}
