// Package auth handles passwords, login sessions and the request's user.
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"quicksand/internal/store"
)

const (
	// OWASP's 2023 recommendation for PBKDF2-HMAC-SHA256.
	pbkdf2Iterations = 600_000
	saltLen          = 16
	keyLen           = 32

	CookieName = "quicksand_session"
	SessionTTL = 7 * 24 * time.Hour

	// MinPasswordLen is enforced when passwords are set.
	MinPasswordLen = 8
)

var ErrInvalidCredentials = errors.New("invalid username or password")

// HashPassword returns a self-describing PBKDF2 hash:
// pbkdf2-sha256$<iterations>$<salt>$<key>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// CreateUser validates and stores a new user.
func CreateUser(ctx context.Context, s *store.Store, username, password string, admin bool) (store.User, error) {
	username = strings.TrimSpace(username)
	if !ValidUsername(username) {
		return store.User{}, errors.New("usernames are 1-64 letters, digits, '.', '_', '-', '@' or '+'")
	}
	if err := ValidatePassword(password); err != nil {
		return store.User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return store.User{}, err
	}
	return s.CreateUser(ctx, username, hash, admin)
}

// ValidUsername allows email addresses, which single sign-on providers may
// use as usernames.
func ValidUsername(name string) bool {
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-@+", c)) {
			return false
		}
	}
	return true
}

func ValidatePassword(password string) error {
	if len(password) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	return nil
}

// A hash every failed lookup is checked against, so unknown usernames take
// as long as wrong passwords.
var dummyHash, _ = HashPassword("quicksand-dummy-password")

type Sessions struct {
	store  *store.Store
	secure bool
}

func NewSessions(s *store.Store, secureCookies bool) *Sessions {
	return &Sessions{store: s, secure: secureCookies}
}

// Login checks credentials and, on success, sets a session cookie.
func (s *Sessions) Login(w http.ResponseWriter, r *http.Request, username, password string) (store.User, error) {
	u, hash, err := s.store.UserCredentials(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		CheckPassword(dummyHash, password)
		return store.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return store.User{}, err
	}
	if !CheckPassword(hash, password) {
		return store.User{}, ErrInvalidCredentials
	}
	return u, s.Start(w, r, u.ID, "")
}

// Start signs a user in with a new session cookie. idToken is the
// provider's ID token for single sign-on sessions.
func (s *Sessions) Start(w http.ResponseWriter, r *http.Request, userID int64, idToken string) error {
	value, expires, err := s.Create(r.Context(), userID, idToken)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secure,
		// Strict keeps the cookie off cross-site requests, which together
		// with JSON-only APIs guards against CSRF.
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// Create stores a new session and returns its cookie value, for clients
// such as qs that keep the cookie themselves.
func (s *Sessions) Create(ctx context.Context, userID int64, idToken string) (string, time.Time, error) {
	token := make([]byte, 32)
	rand.Read(token)
	expires := time.Now().Add(SessionTTL)
	if err := s.store.CreateSession(ctx, tokenHash(token), userID, expires, idToken); err != nil {
		return "", time.Time{}, err
	}
	return base64.RawURLEncoding.EncodeToString(token), expires, nil
}

// Logout ends the request's session and returns its ID token, if it was a
// single sign-on session.
func (s *Sessions) Logout(w http.ResponseWriter, r *http.Request) (idToken string, err error) {
	if token, ok := cookieToken(r); ok {
		if idToken, err = s.store.DeleteSession(r.Context(), tokenHash(token)); err != nil {
			return "", err
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
	})
	return idToken, nil
}

// User returns the user of the request's session, if any.
func (s *Sessions) User(r *http.Request) (store.User, bool) {
	token, ok := cookieToken(r)
	if !ok {
		return store.User{}, false
	}
	u, err := s.store.SessionUser(r.Context(), tokenHash(token))
	return u, err == nil
}

func cookieToken(r *http.Request) ([]byte, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, false
	}
	token, err := base64.RawURLEncoding.DecodeString(c.Value)
	return token, err == nil && len(token) == 32
}

func tokenHash(token []byte) []byte {
	h := sha256.Sum256(token)
	return h[:]
}

type contextKey struct{}

func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}

// FromContext returns the authenticated user set by WithUser.
func FromContext(ctx context.Context) store.User {
	u, _ := ctx.Value(contextKey{}).(store.User)
	return u
}
