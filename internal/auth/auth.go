// Package auth provides the panel's single-operator login: a PBKDF2 password
// credential plus in-memory session tokens.
//
// Sessions deliberately live only in memory. Restarting the panel invalidates
// every login, which is the safe default for a tool that can execute commands
// on a game server; it costs the operator one extra login after an upgrade.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// pbkdf2Iterations follows the OWASP recommendation for PBKDF2-HMAC-SHA256.
const pbkdf2Iterations = 210_000

const (
	saltBytes  = 16
	keyBytes   = 32
	tokenBytes = 32
)

// ErrInvalidCredentials is returned for any failed login, without saying
// whether it was the username or the password that was wrong.
var ErrInvalidCredentials = errors.New("invalid username or password")

// Credential is the stored form of one account's password.
type Credential struct {
	// Username is the name this credential was derived under. It is a record,
	// not an authority: internal/users owns who an account is, and a rename
	// leaves this field behind. Nothing authenticates against it — see
	// VerifyPassword, and note that the paired username+password check this
	// type used to offer is gone precisely so it cannot be reached by accident.
	Username   string `json:"username"`
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
	Iterations int    `json:"iterations"`
}

// NewCredential derives a fresh credential from a plaintext password.
func NewCredential(username, password string) (Credential, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Credential{}, errors.New("username is required")
	}
	if len(password) < 8 {
		return Credential{}, errors.New("password must be at least 8 characters")
	}

	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return Credential{}, fmt.Errorf("generate salt: %w", err)
	}
	key, err := derive(password, salt, pbkdf2Iterations)
	if err != nil {
		return Credential{}, err
	}
	return Credential{
		Username:   username,
		Salt:       hex.EncodeToString(salt),
		Hash:       hex.EncodeToString(key),
		Iterations: pbkdf2Iterations,
	}, nil
}

// IsZero reports whether no password has been configured yet.
func (c Credential) IsZero() bool { return c.Hash == "" || c.Salt == "" }

// VerifyPassword checks a password against this credential.
//
// It says nothing about who the account is: the caller has already found the
// account by name, and a second comparison here would only re-check a field
// that goes stale on a rename. Keeping the lookup honest — spending the same
// CPU whether or not the name existed — is the caller's job, because only the
// caller knows there was no account to look up. See users.Registry.Authenticate.
func (c Credential) VerifyPassword(password string) error {
	if c.IsZero() {
		return ErrInvalidCredentials
	}
	salt, err := hex.DecodeString(c.Salt)
	if err != nil {
		return ErrInvalidCredentials
	}
	want, err := hex.DecodeString(c.Hash)
	if err != nil {
		return ErrInvalidCredentials
	}
	iterations := c.Iterations
	if iterations <= 0 {
		iterations = pbkdf2Iterations
	}

	got, err := derive(password, salt, iterations)
	if err != nil {
		return ErrInvalidCredentials
	}

	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

// Session is an authenticated browser session.
type Session struct {
	Token string
	// UserID is who signed in. The account is looked up by it on every request
	// rather than trusted from here, so deleting or disabling an account takes
	// effect on its next request instead of whenever the session expires.
	UserID string
	// Username is a copy for logging. Never used to find the account: it goes
	// stale the moment somebody is renamed.
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore holds live sessions.
type SessionStore struct {
	ttl time.Duration

	mu       sync.Mutex
	sessions map[string]Session
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &SessionStore{ttl: ttl, sessions: make(map[string]Session)}
}

// TTL is how long a new session stays valid.
func (s *SessionStore) TTL() time.Duration { return s.ttl }

// Create issues a new session token.
func (s *SessionStore) Create(userID, username string) (Session, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return Session{}, fmt.Errorf("generate session token: %w", err)
	}

	now := time.Now()
	sess := Session{
		Token:     hex.EncodeToString(raw),
		UserID:    userID,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}

	s.mu.Lock()
	s.sessions[sess.Token] = sess
	s.mu.Unlock()
	return sess, nil
}

// Validate returns the session for a token, if it exists and has not expired.
func (s *SessionStore) Validate(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[token]
	if !ok {
		return Session{}, false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(s.sessions, token)
		return Session{}, false
	}
	return sess, true
}

// Revoke drops a single session (logout).
func (s *SessionStore) Revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// RevokeUser drops every session belonging to one account and reports how many
// there were. A password change triggers it for its own account, and an
// administrator can trigger it for somebody else's.
func (s *SessionStore) RevokeUser(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for token, sess := range s.sessions {
		if sess.UserID == userID {
			delete(s.sessions, token)
			n++
		}
	}
	return n
}

// GC removes expired sessions; call it periodically.
func (s *SessionStore) GC() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
}

// GeneratePassword returns a readable random password, used to bootstrap the
// first login so the panel is never reachable without credentials.
func GeneratePassword() (string, error) {
	// Ambiguous characters (0/O, 1/l/I) are left out: this password gets read
	// off a terminal and typed into a browser.
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	const length = 20

	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

func derive(password string, salt []byte, iterations int) ([]byte, error) {
	// crypto/pbkdf2 is stdlib as of Go 1.24, which keeps the panel free of a
	// golang.org/x/crypto dependency.
	return pbkdf2.Key(sha256.New, password, salt, iterations, keyBytes)
}
