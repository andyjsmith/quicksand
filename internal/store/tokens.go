package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TokenIdleLimit is how long an API token lasts without being used.
const TokenIdleLimit = 90 * 24 * time.Hour

// Token is an API token's metadata; only a hash of the secret is stored.
type Token struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	// LastUsedAt is zero until the token is first used (updated at most
	// hourly).
	LastUsedAt time.Time `json:"lastUsedAt"`
	// ExpiresAt is zero for tokens that only expire when unused for
	// TokenIdleLimit.
	ExpiresAt time.Time `json:"expiresAt"`
}

const tokenColumns = `id, name, created_at, last_used_at, expires_at`

func scanToken(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	var created, used, expires int64
	err := row.Scan(&t.ID, &t.Name, &created, &used, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	t.CreatedAt = fromUnix(created)
	if used != 0 {
		t.LastUsedAt = fromUnix(used)
	}
	if expires != 0 {
		t.ExpiresAt = fromUnix(expires)
	}
	return t, err
}

// CreateToken stores a token for a user; expires is zero for none.
func (s *Store) CreateToken(ctx context.Context, userID int64, name string, hash []byte, expires time.Time) (Token, error) {
	return scanToken(s.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)
		 RETURNING `+tokenColumns,
		userID, name, hash, unix(time.Now()), unixOrZero(expires)))
}

// TokenUser returns the user a live token belongs to, and the token's ID,
// and notes its use.
func (s *Store) TokenUser(ctx context.Context, hash []byte) (User, int64, error) {
	now := time.Now()
	var tokenID, used int64
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumnsOf("u")+`, t.id, t.last_used_at FROM api_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = ? AND (t.expires_at = 0 OR t.expires_at > ?) AND max(t.created_at, t.last_used_at) > ?`,
		hash, unix(now), unix(now.Add(-TokenIdleLimit))), &tokenID, &used)
	if err != nil {
		return User{}, 0, err
	}
	if now.Sub(fromUnix(used)) > time.Hour {
		s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, unix(now), tokenID)
	}
	return u, tokenID, nil
}

// Tokens lists a user's tokens, newest first.
func (s *Store) Tokens(ctx context.Context, userID int64) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+tokenColumns+` FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// DeleteToken revokes one of a user's tokens.
func (s *Store) DeleteToken(ctx context.Context, userID, id int64) error {
	return expectRow(s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID))
}

// DeleteUserTokens revokes all of a user's tokens.
func (s *Store) DeleteUserTokens(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id = ?`, userID)
	return err
}

// DeleteDeadTokens removes expired tokens and ones unused for
// TokenIdleLimit.
func (s *Store) DeleteDeadTokens(ctx context.Context) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE (expires_at != 0 AND expires_at <= ?) OR max(created_at, last_used_at) <= ?`,
		unix(now), unix(now.Add(-TokenIdleLimit)))
	return err
}
