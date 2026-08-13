package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SSHKey is a public key that may reach the repositories over SSH.
type SSHKey struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	PublicKey   string     `json:"publicKey"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
}

const sshKeyCols = `id, name, public_key, fingerprint, created_at, last_used_at`

func scanSSHKey(r scanner) (*SSHKey, error) {
	var k SSHKey
	if err := r.Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt, &k.LastUsedAt); err != nil {
		return nil, norm(err)
	}
	return &k, nil
}

func (s *Store) CreateSSHKey(ctx context.Context, ownerID uuid.UUID, name, publicKey, fingerprint string) (*SSHKey, error) {
	return scanSSHKey(s.pool.QueryRow(ctx, `
		INSERT INTO ssh_keys (owner_id, name, public_key, fingerprint)
		VALUES ($1,$2,$3,$4) RETURNING `+sshKeyCols,
		ownerID, name, publicKey, fingerprint))
}

func (s *Store) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sshKeyCols+` FROM ssh_keys ORDER BY created_at`)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []SSHKey{}
	for rows.Next() {
		k, err := scanSSHKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, norm(rows.Err())
}

// SSHKeyOwner resolves the key a connection came in with and notes the use.
func (s *Store) SSHKeyOwner(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var owner uuid.UUID
	err := s.pool.QueryRow(ctx,
		`UPDATE ssh_keys SET last_used_at=now() WHERE id=$1 RETURNING owner_id`, id).Scan(&owner)
	return owner, norm(err)
}

func (s *Store) DeleteSSHKey(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM ssh_keys WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
