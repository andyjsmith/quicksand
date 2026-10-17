package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"isAdmin"`
	CreatedAt time.Time `json:"createdAt"`
	// SSO is set for users who sign in with single sign-on.
	SSO bool `json:"sso"`
	// HasPassword is unset for single sign-on users without a password.
	HasPassword bool `json:"hasPassword"`
}

const userColumns = `id, username, is_admin, created_at, oidc_subject IS NOT NULL, password_hash != ''`

// scanUser scans userColumns, then extra.
func scanUser(row interface{ Scan(...any) error }, extra ...any) (User, error) {
	var u User
	var created int64
	err := row.Scan(append([]any{&u.ID, &u.Username, &u.IsAdmin, &created, &u.SSO, &u.HasPassword}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.CreatedAt = fromUnix(created)
	return u, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, admin bool) (User, error) {
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?)
		 RETURNING `+userColumns,
		username, passwordHash, admin, unix(time.Now()))
	u, err := scanUser(row)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return User{}, ErrConflict
	}
	return u, err
}

func (s *Store) User(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// UserCredentials returns a user and their password hash for logging in.
func (s *Store) UserCredentials(ctx context.Context, username string) (User, string, error) {
	var hash string
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+`, password_hash FROM users WHERE username = ?`, username,
	), &hash)
	return u, hash, err
}

// UserBySubject returns the single sign-on user with an OIDC subject.
func (s *Store) UserBySubject(ctx context.Context, subject string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE oidc_subject = ?`, subject))
}

// CreateSSOUser stores a new single sign-on user, without a password.
func (s *Store) CreateSSOUser(ctx context.Context, username, subject string, admin bool) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, is_admin, created_at, oidc_subject) VALUES (?, '', ?, ?, ?)
		 RETURNING `+userColumns,
		username, admin, unix(time.Now()), subject))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return User{}, ErrConflict
	}
	return u, err
}

// LinkSubject makes an existing user a single sign-on user.
func (s *Store) LinkSubject(ctx context.Context, id int64, subject string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET oidc_subject = ? WHERE id = ?`, subject, id)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return expectRow(res, err)
}

// SetUsername renames a user; ErrConflict if the name is taken.
func (s *Store) SetUsername(ctx context.Context, id int64, username string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET username = ? WHERE id = ?`, username, id)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return expectRow(res, err)
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) SetPassword(ctx context.Context, id int64, passwordHash string) error {
	return expectRow(s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id))
}

func (s *Store) SetAdmin(ctx context.Context, id int64, admin bool) error {
	return expectRow(s.db.ExecContext(ctx, `UPDATE users SET is_admin = ? WHERE id = ?`, admin, id))
}

// DeleteUser removes a user and their sessions. It fails with ErrConflict
// while the user still owns VMs.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	var owned int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM vms WHERE owner_id = ?`, id).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		return ErrConflict
	}
	return expectRow(s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id))
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE is_admin`).Scan(&n)
	return n, err
}

func expectRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
