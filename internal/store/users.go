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
}

const userColumns = `id, username, is_admin, created_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created int64
	err := row.Scan(&u.ID, &u.Username, &u.IsAdmin, &created)
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
	var u User
	var hash string
	var created int64
	err := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+`, password_hash FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.IsAdmin, &created, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	u.CreatedAt = fromUnix(created)
	return u, hash, err
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
