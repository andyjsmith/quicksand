package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// AuditEntry records something a user did.
type AuditEntry struct {
	ID int64     `json:"id"`
	At time.Time `json:"at"`
	// UserID is 0 for failed sign-ins. Username is kept as it was, as
	// users can be renamed or deleted.
	UserID   int64  `json:"userId,omitempty"`
	Username string `json:"username"`
	// Action is what was done, e.g. "vm.deploy" or "user.delete".
	Action string `json:"action"`
	// Target is what it was done to, e.g. a VM or user name.
	Target string `json:"target"`
	Detail string `json:"detail"`
	IP     string `json:"ip"`
}

func (s *Store) AddAudit(ctx context.Context, e AuditEntry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	var userID sql.NullInt64
	if e.UserID != 0 {
		userID = sql.NullInt64{Int64: e.UserID, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (at, user_id, username, action, target, detail, ip) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		unix(e.At), userID, e.Username, e.Action, e.Target, e.Detail, e.IP)
	return err
}

// Audit returns up to limit entries older than entry before (0: the
// newest), newest first, that contain query (if set) in their user,
// action, target or detail.
func (s *Store) Audit(ctx context.Context, before int64, limit int, query string) ([]AuditEntry, error) {
	where, args := []string{"1"}, []any{}
	if before > 0 {
		where, args = append(where, "id < ?"), append(args, before)
	}
	if query != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(query) + "%"
		where = append(where, `(username LIKE ? ESCAPE '\' OR action LIKE ? ESCAPE '\' OR target LIKE ? ESCAPE '\' OR detail LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, coalesce(user_id, 0), username, action, target, detail, ip FROM audit_log
		 WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.UserID, &e.Username, &e.Action, &e.Target, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		e.At = fromUnix(at)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// DeleteOldAudit removes entries older than retention (0: none).
func (s *Store) DeleteOldAudit(ctx context.Context, retention time.Duration) error {
	if retention <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE at < ?`, unix(time.Now().Add(-retention)))
	return err
}
