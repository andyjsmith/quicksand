package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"quicksand/internal/store"
)

// TokenPrefix starts API token secrets, so they're recognizable (and
// findable by secret scanners).
const TokenPrefix = "qst_"

// newToken returns a new API token's secret and the hash stored for it.
func newToken() (secret string, hash []byte) {
	b := make([]byte, 32)
	rand.Read(b)
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b), tokenHash(b)
}

// bearerToken returns the hash of the request's API token, if it has one.
func bearerToken(r *http.Request) ([]byte, bool) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "+TokenPrefix)
	if !ok {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(secret)
	return tokenHash(b), err == nil && len(b) == 32
}

// CreateToken makes an API token for a user and returns it with its
// secret, which isn't stored. expires is zero for a token that lasts until
// it goes unused for store.TokenIdleLimit.
func (s *Sessions) CreateToken(ctx context.Context, userID int64, name string, expires time.Time) (store.Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return store.Token{}, "", errors.New("token names are 1-100 characters")
	}
	secret, hash := newToken()
	t, err := s.store.CreateToken(ctx, userID, name, hash, expires)
	return t, secret, err
}
