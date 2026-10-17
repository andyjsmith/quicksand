package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateSession stores a login session. Only a hash of the token is kept, so
// a leaked database does not leak usable sessions. idToken is the provider's
// ID token for single sign-on sessions, used to sign out there too.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, expires time.Time, idToken string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, id_token) VALUES (?, ?, ?, ?, ?)`,
		tokenHash, userID, unix(time.Now()), unix(expires), idToken)
	return err
}

// SessionUser returns the user a live session belongs to.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE id = (SELECT user_id FROM sessions WHERE token_hash = ? AND expires_at > ?)`,
		tokenHash, unix(time.Now())))
}

// DeleteSession ends a session, returning its ID token (if any).
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) (idToken string, err error) {
	err = s.db.QueryRowContext(ctx, `DELETE FROM sessions WHERE token_hash = ? RETURNING id_token`, tokenHash).Scan(&idToken)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return idToken, err
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
