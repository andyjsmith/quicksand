// Package store persists Quicksand's users, sessions and deployed VMs in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

type Store struct {
	db *sql.DB
	// createMu makes CreateVM's limit checks and insert atomic: SQLite's
	// deferred transactions would let two concurrent ones both pass.
	createMu sync.Mutex
}

// migrations are applied in order; the database's user_version records how
// many have run. Never edit an applied migration; append a new one.
var migrations = []string{
	`CREATE TABLE users (
		id            INTEGER PRIMARY KEY,
		username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
		password_hash TEXT    NOT NULL,
		is_admin      INTEGER NOT NULL DEFAULT 0,
		created_at    INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		token_hash BLOB    PRIMARY KEY,
		user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	);
	CREATE INDEX sessions_user ON sessions(user_id);
	CREATE TABLE vms (
		id          TEXT    PRIMARY KEY,
		owner_id    INTEGER NOT NULL REFERENCES users(id),
		name        TEXT    NOT NULL,
		template_id TEXT    NOT NULL,
		node        TEXT    NOT NULL,
		vmid        INTEGER NOT NULL UNIQUE,
		cores       INTEGER NOT NULL,
		memory_mib  INTEGER NOT NULL,
		network_id  TEXT    NOT NULL,
		state       TEXT    NOT NULL,
		step        TEXT    NOT NULL DEFAULT '',
		progress    REAL    NOT NULL DEFAULT 0,
		error       TEXT    NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL
	);
	CREATE INDEX vms_owner ON vms(owner_id);`,
	// 2: agent addresses.
	`ALTER TABLE vms ADD COLUMN agent_ip TEXT NOT NULL DEFAULT '';
	CREATE UNIQUE INDEX vms_agent_ip ON vms(agent_ip) WHERE agent_ip != '';`,
	// 3: expiry (0: none) and how many times it was extended.
	`ALTER TABLE vms ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE vms ADD COLUMN extends INTEGER NOT NULL DEFAULT 0;`,
	// 4: link networks, and each VM's network and address on them.
	`CREATE TABLE link_networks (
		id         INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		vlan       INTEGER NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	);
	ALTER TABLE vms ADD COLUMN link_id INTEGER REFERENCES link_networks(id);
	ALTER TABLE vms ADD COLUMN link_ip TEXT NOT NULL DEFAULT '';
	CREATE INDEX vms_link ON vms(link_id);
	CREATE UNIQUE INDEX vms_link_ip ON vms(link_ip) WHERE link_ip != '';`,
	// 5: user firewall settings (JSON; empty: never set).
	`ALTER TABLE vms ADD COLUMN firewall TEXT NOT NULL DEFAULT '';`,
	// 6: single sign-on: users' OIDC subjects (password_hash is empty for
	// users without a password), and the ID token of single sign-on
	// sessions, to sign out at the provider.
	`ALTER TABLE users ADD COLUMN oidc_subject TEXT;
	CREATE UNIQUE INDEX users_oidc_subject ON users(oidc_subject) WHERE oidc_subject IS NOT NULL;
	ALTER TABLE sessions ADD COLUMN id_token TEXT NOT NULL DEFAULT '';`,
	// 7: API tokens (qs signs in with one), and the audit log, whose
	// entries outlive their users.
	`CREATE TABLE api_tokens (
		id           INTEGER PRIMARY KEY,
		user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name         TEXT    NOT NULL,
		token_hash   BLOB    NOT NULL UNIQUE,
		created_at   INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0,
		expires_at   INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX api_tokens_user ON api_tokens(user_id);
	CREATE TABLE audit_log (
		id       INTEGER PRIMARY KEY,
		at       INTEGER NOT NULL,
		user_id  INTEGER,
		username TEXT    NOT NULL,
		action   TEXT    NOT NULL,
		target   TEXT    NOT NULL DEFAULT '',
		detail   TEXT    NOT NULL DEFAULT '',
		ip       TEXT    NOT NULL DEFAULT ''
	);
	CREATE INDEX audit_log_at ON audit_log(at);`,
}

func Open(path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; a single connection avoids
	// SQLITE_BUSY between our own goroutines.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func unix(t time.Time) int64 { return t.Unix() }

func fromUnix(v int64) time.Time { return time.Unix(v, 0).UTC() }
