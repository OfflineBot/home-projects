package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

const userCols = `id, username, password_hash, display_name, totp_secret, totp_enabled, is_owner, created_at`

func scanUser(r scanner) (*model.User, error) {
	var u model.User
	var totp *string
	err := r.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &totp, &u.TOTPEnabled, &u.IsOwner, &u.CreatedAt)
	if err != nil {
		return nil, norm(err)
	}
	u.TOTPSecret = strp(totp)
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, norm(err)
}

func (s *Store) CreateUser(ctx context.Context, username, hash, displayName string, owner bool) (*model.User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name, is_owner)
		VALUES ($1, $2, $3, $4) RETURNING `+userCols,
		username, hash, displayName, owner)
	return scanUser(row)
}

func (s *Store) UserByName(ctx context.Context, username string) (*model.User, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username)=lower($1)`, username)
	return scanUser(row)
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id)
	return scanUser(row)
}

// OwnerID returns the single owner account's id — every row carries an
// owner_id from day one, even while there is only one user.
func (s *Store) OwnerID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM users ORDER BY is_owner DESC, created_at ASC LIMIT 1`).Scan(&id)
	return id, norm(err)
}

func (s *Store) SetPassword(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, id, hash)
	return norm(err)
}

func (s *Store) SetTOTP(ctx context.Context, id uuid.UUID, secret string, enabled bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET totp_secret=$2, totp_enabled=$3, updated_at=now() WHERE id=$1`,
		id, nilIfEmpty(secret), enabled)
	return norm(err)
}

// ---------------------------------------------------------------- sessions

const sessionCols = `id, user_id, user_agent, ip, created_at, last_used_at, expires_at, stepup_at`

func scanSession(r scanner) (*model.Session, error) {
	var s model.Session
	err := r.Scan(&s.ID, &s.UserID, &s.UserAgent, &s.IP, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt, &s.StepUpAt)
	if err != nil {
		return nil, norm(err)
	}
	return &s, nil
}

func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, fgpHash, refreshHash, ua, ip string, expires time.Time) (*model.Session, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, fgp_hash, refresh_hash, user_agent, ip, expires_at, stepup_at)
		VALUES ($1,$2,$3,$4,$5,$6, now()) RETURNING `+sessionCols,
		userID, fgpHash, refreshHash, ua, ip, expires)
	return scanSession(row)
}

// SessionAuth returns what the middleware needs to validate a request: the
// session plus the two hashes it compares against.
func (s *Store) SessionAuth(ctx context.Context, id uuid.UUID) (*model.Session, string, string, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+sessionCols+`, fgp_hash, refresh_hash FROM sessions
		 WHERE id=$1 AND revoked_at IS NULL AND expires_at > now()`, id)
	var sess model.Session
	var fgp, refresh string
	err := row.Scan(&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP, &sess.CreatedAt,
		&sess.LastUsedAt, &sess.ExpiresAt, &sess.StepUpAt, &fgp, &refresh)
	if err != nil {
		return nil, "", "", norm(err)
	}
	return &sess, fgp, refresh, nil
}

func (s *Store) TouchSession(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_used_at=now() WHERE id=$1`, id)
}

func (s *Store) MarkStepUp(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET stepup_at=now() WHERE id=$1`, id)
	return norm(err)
}

func (s *Store) RotateRefresh(ctx context.Context, id uuid.UUID, refreshHash string, expires time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET refresh_hash=$2, expires_at=$3, last_used_at=now() WHERE id=$1`,
		id, refreshHash, expires)
	return norm(err)
}

func (s *Store) ListSessions(ctx context.Context, userID uuid.UUID) ([]model.Session, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+sessionCols+` FROM sessions
		 WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > now()
		 ORDER BY last_used_at DESC`, userID)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Session{}
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, norm(rows.Err())
}

func (s *Store) RevokeSession(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeAllSessions(ctx context.Context, userID uuid.UUID, except *uuid.UUID) error {
	if except != nil {
		_, err := s.pool.Exec(ctx,
			`UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`,
			userID, *except)
		return norm(err)
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID)
	return norm(err)
}

// ------------------------------------------------------------------- audit

func (s *Store) Audit(ctx context.Context, userID *uuid.UUID, action, subject, ip string, detail any) {
	body := []byte(`{}`)
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			body = b
		}
	}
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO audit_log (user_id, action, subject, detail, ip) VALUES ($1,$2,$3,$4,$5)`,
		userID, action, subject, body, ip)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, action, subject, detail, ip, created_at FROM audit_log
		 ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		if err := rows.Scan(&e.ID, &e.Action, &e.Subject, &e.Detail, &e.IP, &e.CreatedAt); err != nil {
			return nil, norm(err)
		}
		out = append(out, e)
	}
	return out, norm(rows.Err())
}

// --------------------------------------------------------- failed attempts

// RecordAttempt notes one authentication attempt. scope is "login",
// "project-password", "group-password" or "git".
func (s *Store) RecordAttempt(ctx context.Context, scope, subject, ip string, ok bool) {
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO auth_attempts (scope, subject, ok, ip) VALUES ($1,$2,$3,$4)`,
		scope, subject, ok, ip)
}

// RecentFailures counts failed attempts in the given window — the basis for
// throttling logins, project passwords and git basic auth alike.
func (s *Store) RecentFailures(ctx context.Context, scope, subject string, window time.Duration) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM auth_attempts
		 WHERE scope=$1 AND subject=$2 AND ok=false AND created_at > now() - $3::interval`,
		scope, subject, window.String()).Scan(&n)
	return n, norm(err)
}

// ------------------------------------------------------------------ tokens

const tokenCols = `id, name, scope, project_id, group_id, created_at, last_used_at, expires_at, revoked_at`

func scanToken(r scanner) (*model.Token, error) {
	var t model.Token
	err := r.Scan(&t.ID, &t.Name, &t.Scope, &t.ProjectID, &t.GroupID, &t.CreatedAt,
		&t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt)
	if err != nil {
		return nil, norm(err)
	}
	return &t, nil
}

func (s *Store) CreateToken(ctx context.Context, ownerID uuid.UUID, name, hash, scope string, projectID, groupID *uuid.UUID, expires *time.Time) (*model.Token, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (owner_id, name, token_hash, scope, project_id, group_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+tokenCols,
		ownerID, name, hash, scope, projectID, groupID, expires)
	return scanToken(row)
}

func (s *Store) ListTokens(ctx context.Context, ownerID uuid.UUID) ([]model.Token, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tokenCols+` FROM api_tokens WHERE owner_id=$1 AND revoked_at IS NULL
		 ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, norm(rows.Err())
}

// TokenByHash resolves a presented token and marks it as used.
func (s *Store) TokenByHash(ctx context.Context, hash string) (*model.Token, uuid.UUID, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE api_tokens SET last_used_at=now()
		 WHERE token_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())
		 RETURNING `+tokenCols+`, owner_id`, hash)
	var t model.Token
	var owner uuid.UUID
	err := row.Scan(&t.ID, &t.Name, &t.Scope, &t.ProjectID, &t.GroupID, &t.CreatedAt,
		&t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &owner)
	if err != nil {
		return nil, uuid.Nil, norm(err)
	}
	return &t, owner, nil
}

func (s *Store) RevokeToken(ctx context.Context, ownerID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_tokens SET revoked_at=now() WHERE id=$1 AND owner_id=$2 AND revoked_at IS NULL`,
		id, ownerID)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- settings

// Setting reads a key/value entry. Missing keys are not an error; they come
// back as an empty raw message.
func (s *Store) Setting(ctx context.Context, key string) (json.RawMessage, error) {
	var v json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&v)
	if err != nil {
		if errors.Is(norm(err), ErrNotFound) {
			return nil, nil
		}
		return nil, norm(err)
	}
	return v, nil
}

func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1,$2)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, body)
	return norm(err)
}

// Blocked is one subject that has run into the throttle.
type Blocked struct {
	Subject  string    `json:"subject"`
	Failures int       `json:"failures"`
	LastAt   time.Time `json:"lastAt"`
	IP       string    `json:"ip"`
}

// BlockedSubjects lists what is currently locked out, so a block can be seen
// rather than only felt.
func (s *Store) BlockedSubjects(ctx context.Context, scope string, window time.Duration, threshold int) ([]Blocked, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT subject, count(*), max(created_at), max(ip)
		FROM auth_attempts
		WHERE scope=$1 AND ok=false AND created_at > now() - $2::interval
		GROUP BY subject HAVING count(*) >= $3
		ORDER BY max(created_at) DESC`, scope, window.String(), threshold)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []Blocked{}
	for rows.Next() {
		var b Blocked
		if err := rows.Scan(&b.Subject, &b.Failures, &b.LastAt, &b.IP); err != nil {
			return nil, norm(err)
		}
		out = append(out, b)
	}
	return out, norm(rows.Err())
}

// ClearAttempts lifts a block. Locking yourself out by mistyping a password is
// ordinary; having no way back would not be.
func (s *Store) ClearAttempts(ctx context.Context, scope, subject string) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM auth_attempts WHERE scope=$1 AND subject=$2 AND ok=false`, scope, subject)
	if err != nil {
		return 0, norm(err)
	}
	return int(tag.RowsAffected()), nil
}
