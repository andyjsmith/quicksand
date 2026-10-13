package store

import (
	"context"
	"time"
)

// CreateSession stores a login session. Only a hash of the token is kept, so
// a leaked database does not leak usable sessions.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, unix(time.Now()), unix(expires))
	return err
}

// SessionUser returns the user a live session belongs to.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.is_admin, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash, unix(time.Now())))
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions logs a user out everywhere, e.g. after a password change.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, unix(time.Now()))
	return err
}
