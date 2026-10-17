package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	u, _ := s.CreateUser(ctx, "alice", "hash", false)
	other, _ := s.CreateUser(ctx, "bob", "hash", false)

	tok, err := s.CreateToken(ctx, u.ID, "qs on laptop", []byte("h1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got, id, err := s.TokenUser(ctx, []byte("h1")); err != nil || got.ID != u.ID || id != tok.ID {
		t.Fatalf("TokenUser = %+v, %d, %v", got, id, err)
	}
	if list, _ := s.Tokens(ctx, u.ID); len(list) != 1 || list[0].LastUsedAt.IsZero() {
		t.Fatalf("after use, Tokens = %+v; want it marked used", list)
	}
	if err := s.DeleteToken(ctx, other.ID, tok.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user revoking the token: %v, want ErrNotFound", err)
	}

	// Expired, and unused for too long.
	s.CreateToken(ctx, u.ID, "expired", []byte("h2"), time.Now().Add(-time.Minute))
	s.CreateToken(ctx, u.ID, "idle", []byte("h3"), time.Time{})
	s.db.Exec(`UPDATE api_tokens SET created_at = ? WHERE name = 'idle'`, unix(time.Now().Add(-TokenIdleLimit-time.Hour)))
	for _, h := range []string{"h2", "h3"} {
		if _, _, err := s.TokenUser(ctx, []byte(h)); !errors.Is(err, ErrNotFound) {
			t.Errorf("token %s: %v, want ErrNotFound", h, err)
		}
	}
	if err := s.DeleteDeadTokens(ctx); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Tokens(ctx, u.ID); len(list) != 1 || list[0].Name != "qs on laptop" {
		t.Fatalf("after cleanup, Tokens = %+v", list)
	}
	if err := s.DeleteUserTokens(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TokenUser(ctx, []byte("h1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token: %v", err)
	}
}

func TestAudit(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	for i := range 5 {
		if err := s.AddAudit(ctx, AuditEntry{UserID: 1, Username: "alice", Action: "vm.deploy", Target: fmt.Sprintf("vm%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	s.AddAudit(ctx, AuditEntry{Username: "mallory", Action: "login.failed", Detail: "50%_off", IP: "10.0.0.9"})

	page, err := s.Audit(ctx, 0, 3, "")
	if err != nil || len(page) != 3 || page[0].Action != "login.failed" || page[0].UserID != 0 {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	next, _ := s.Audit(ctx, page[2].ID, 3, "")
	if len(next) != 3 || next[0].Target != "vm2" {
		t.Fatalf("second page = %+v", next)
	}
	if got, _ := s.Audit(ctx, 0, 10, "vm3"); len(got) != 1 || got[0].Target != "vm3" {
		t.Fatalf("filtered = %+v", got)
	}
	// LIKE wildcards in the query are literal.
	if got, _ := s.Audit(ctx, 0, 10, "%_"); len(got) != 1 || got[0].Username != "mallory" {
		t.Fatalf("filtered on %%_ = %+v", got)
	}

	s.AddAudit(ctx, AuditEntry{At: time.Now().Add(-48 * time.Hour), Username: "alice", Action: "logout"})
	if err := s.DeleteOldAudit(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Audit(ctx, 0, 100, ""); len(got) != 7 {
		t.Fatalf("retention 0 kept %d entries, want all 7", len(got))
	}
	s.DeleteOldAudit(ctx, 24*time.Hour)
	if got, _ := s.Audit(ctx, 0, 100, ""); len(got) != 6 {
		t.Fatalf("after a 1-day retention, %d entries, want 6", len(got))
	}
}
