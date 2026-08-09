package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// devicePrefix marks a string as a HyperCraft device token. It carries no
// security weight: it is there so a token that leaks into a log or a paste is
// recognisable as a credential instead of looking like any other hex blob.
const devicePrefix = "hcd_"

const (
	deviceTokenBytes = 32
	deviceIDBytes    = 8
	maxDeviceNameLen = 64
)

// ErrDeviceNameRequired and friends are returned by CleanDeviceName; handlers
// surface them as 400s rather than mapping each one.
var ErrDeviceNameRequired = errors.New("device name is required")

// DeviceToken is a long-lived credential belonging to one native client.
//
// It is deliberately not a Session. Sessions live in memory and die with the
// process, which is the right trade for a browser — the operator is sitting at
// it — but wrong for a phone app, which would be signed out every time the
// panel restarts to install an update.
//
// The token is stored as a plain SHA-256 digest rather than a PBKDF2 hash. What
// is being protected is 32 bytes from crypto/rand, not a password a human
// chose, so there is no dictionary to run and no reason to pay for a slow KDF.
// The fast digest is also what lets Validate index straight to the right device
// instead of testing candidates one at a time.
type DeviceToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`
	// LastUsed is best effort. It moves in memory on every authenticated
	// request and only reaches disk when the panel is next persisted, because a
	// write per API call would be a poor trade for a field that exists so the
	// operator can recognise a device they no longer use.
	LastUsed time.Time `json:"lastUsed,omitempty"`
}

// DeviceStore holds the paired clients. It is the runtime owner of the list;
// config.Panel is only where it is parked between runs.
type DeviceStore struct {
	mu     sync.RWMutex
	byID   map[string]DeviceToken
	byHash map[string]string // token digest -> device ID
	// dirty records that a LastUsed moved since the last Snapshot, so the
	// periodic flush can skip writing a file that has not changed.
	dirty bool
}

// NewDeviceStore seeds the store from the persisted list.
func NewDeviceStore(devices []DeviceToken) *DeviceStore {
	s := &DeviceStore{
		byID:   make(map[string]DeviceToken, len(devices)),
		byHash: make(map[string]string, len(devices)),
	}
	for _, dev := range devices {
		// panel.json is a file the operator is invited to read and edit, so a
		// half-written entry is possible. Dropping it beats serving a device
		// that can never be matched or revoked.
		if dev.ID == "" || dev.Hash == "" {
			continue
		}
		s.byID[dev.ID] = dev
		s.byHash[dev.Hash] = dev.ID
	}
	return s
}

// Issue mints a token for a new device. The plaintext is returned exactly once:
// the store keeps only its digest, so it cannot be recovered afterwards.
func (s *DeviceStore) Issue(name string) (DeviceToken, string, error) {
	name, err := CleanDeviceName(name)
	if err != nil {
		return DeviceToken{}, "", err
	}

	raw := make([]byte, deviceTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return DeviceToken{}, "", fmt.Errorf("generate device token: %w", err)
	}
	id := make([]byte, deviceIDBytes)
	if _, err := rand.Read(id); err != nil {
		return DeviceToken{}, "", fmt.Errorf("generate device id: %w", err)
	}

	token := devicePrefix + hex.EncodeToString(raw)
	dev := DeviceToken{
		ID:        hex.EncodeToString(id),
		Name:      name,
		Hash:      HashDeviceToken(token),
		CreatedAt: time.Now(),
	}

	s.mu.Lock()
	s.byID[dev.ID] = dev
	s.byHash[dev.Hash] = dev.ID
	s.mu.Unlock()

	return dev, token, nil
}

// Validate resolves a presented token to its device and records the use.
//
// The lookup is a map hit on the digest rather than a constant-time compare.
// That is safe here in a way it would not be for a password: an attacker cannot
// walk the digest byte by byte, because they cannot steer the preimage — the
// only way to produce a digest close to a stored one is to already hold the
// token that makes it.
func (s *DeviceStore) Validate(token string) (DeviceToken, bool) {
	if !strings.HasPrefix(token, devicePrefix) {
		return DeviceToken{}, false
	}
	hash := HashDeviceToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.byHash[hash]
	if !ok {
		return DeviceToken{}, false
	}
	dev := s.byID[id]
	dev.LastUsed = time.Now()
	s.byID[id] = dev
	s.dirty = true
	return dev, true
}

// Get returns one device by ID.
func (s *DeviceStore) Get(id string) (DeviceToken, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dev, ok := s.byID[id]
	return dev, ok
}

// List returns every device in a stable order, for the API.
func (s *DeviceStore) List() []DeviceToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sortedLocked()
}

// Snapshot returns the list for persisting and clears the dirty flag: the
// timestamps the caller is about to write are the ones it is being handed.
func (s *DeviceStore) Snapshot() []DeviceToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = false
	return s.sortedLocked()
}

// Dirty reports whether a token has been used since the last Snapshot.
func (s *DeviceStore) Dirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dirty
}

// Revoke unpairs one device and reports whether it existed.
func (s *DeviceStore) Revoke(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	dev, ok := s.byID[id]
	if !ok {
		return false
	}
	delete(s.byID, id)
	delete(s.byHash, dev.Hash)
	return true
}

// RevokeAll unpairs every device and reports how many there were. A password
// change triggers it: the operator is saying the old credential should stop
// working, and every device token was minted by presenting that credential.
func (s *DeviceStore) RevokeAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.byID)
	s.byID = make(map[string]DeviceToken)
	s.byHash = make(map[string]string)
	return n
}

func (s *DeviceStore) sortedLocked() []DeviceToken {
	out := make([]DeviceToken, 0, len(s.byID))
	for _, dev := range s.byID {
		out = append(out, dev)
	}
	// Oldest first, with the ID breaking ties so two devices paired in the same
	// instant do not swap places between calls.
	slices.SortFunc(out, func(a, b DeviceToken) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// HashDeviceToken is the stored form of a device token.
func HashDeviceToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CleanDeviceName normalises the label an operator gives a device. The name is
// shown back to them in the device list and in the panel log, so control
// characters are rejected rather than left to garble the output.
func CleanDeviceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrDeviceNameRequired
	}
	if !utf8.ValidString(name) {
		return "", errors.New("device name must be valid UTF-8")
	}
	if utf8.RuneCountInString(name) > maxDeviceNameLen {
		return "", fmt.Errorf("device name must be at most %d characters", maxDeviceNameLen)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("device name must not contain control characters")
		}
	}
	return name, nil
}
