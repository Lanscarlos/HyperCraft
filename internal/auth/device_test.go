package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeviceIssueAndValidate(t *testing.T) {
	store := NewDeviceStore(nil)

	dev, token, err := store.Issue("u1", "Lans 的手机")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !strings.HasPrefix(token, devicePrefix) {
		t.Errorf("token %q does not carry the %q prefix", token, devicePrefix)
	}
	if strings.Contains(dev.Hash, token) || dev.Hash == token {
		t.Error("the stored hash is the token itself")
	}

	got, ok := store.Validate(token)
	if !ok {
		t.Fatal("a freshly issued token did not validate")
	}
	if got.ID != dev.ID || got.Name != "Lans 的手机" {
		t.Errorf("Validate returned %+v, want the device just issued", got)
	}
	if got.LastUsed == nil {
		t.Error("Validate did not record the use")
	}
}

func TestDeviceValidateRejects(t *testing.T) {
	store := NewDeviceStore(nil)
	dev, token, err := store.Issue("u1", "phone")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cases := map[string]string{
		"empty":            "",
		"no prefix":        strings.TrimPrefix(token, devicePrefix),
		"wrong body":       devicePrefix + strings.Repeat("0", 64),
		"the stored hash":  dev.Hash,
		"prefix only":      devicePrefix,
		"truncated token":  token[:len(token)-1],
		"session-ish blob": "0123456789abcdef",
	}
	for name, candidate := range cases {
		if _, ok := store.Validate(candidate); ok {
			t.Errorf("%s: validated when it should not have", name)
		}
	}
}

func TestDeviceRevoke(t *testing.T) {
	store := NewDeviceStore(nil)
	dev, token, err := store.Issue("u1", "phone")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if !store.Revoke(dev.ID) {
		t.Fatal("Revoke reported the device was not there")
	}
	if _, ok := store.Validate(token); ok {
		t.Error("a revoked token still validates")
	}
	if store.Revoke(dev.ID) {
		t.Error("revoking twice reported success the second time")
	}
}

func TestDeviceRevokeAll(t *testing.T) {
	store := NewDeviceStore(nil)
	var tokens []string
	for _, name := range []string{"phone", "tablet", "laptop"} {
		_, token, err := store.Issue("u1", name)
		if err != nil {
			t.Fatalf("Issue(%s): %v", name, err)
		}
		tokens = append(tokens, token)
	}

	if n := store.RevokeUser("u1"); n != 3 {
		t.Errorf("RevokeUser reported %d devices, want 3", n)
	}
	for _, token := range tokens {
		if _, ok := store.Validate(token); ok {
			t.Error("a token survived RevokeUser")
		}
	}
	if got := store.List(); len(got) != 0 {
		t.Errorf("List returned %d devices after RevokeUser", len(got))
	}
}

// RevokeUser must leave everybody else paired: one person changing their
// password is not a statement about anyone else's phone.
func TestDeviceRevokeUserSparesOthers(t *testing.T) {
	store := NewDeviceStore(nil)
	_, mine, err := store.Issue("u1", "my phone")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	_, theirs, err := store.Issue("u2", "their phone")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if n := store.RevokeUser("u1"); n != 1 {
		t.Errorf("RevokeUser reported %d devices, want 1", n)
	}
	if _, ok := store.Validate(mine); ok {
		t.Error("u1's token survived")
	}
	if _, ok := store.Validate(theirs); !ok {
		t.Error("u2's token was revoked along with u1's")
	}
}

// AdoptOrphanDevices is the migration: tokens minted before the panel had
// accounts belong to whoever the single operator was.
func TestAdoptOrphanDevices(t *testing.T) {
	devices := []DeviceToken{
		{ID: "a", Name: "old phone"},
		{ID: "b", UserID: "u2", Name: "new phone"},
	}

	if n := AdoptOrphanDevices(devices, "u1"); n != 1 {
		t.Fatalf("AdoptOrphanDevices reported %d devices, want 1", n)
	}
	if devices[0].UserID != "u1" {
		t.Errorf("orphan went to %q, want u1", devices[0].UserID)
	}
	if devices[1].UserID != "u2" {
		t.Errorf("an owned device was reassigned to %q", devices[1].UserID)
	}

	// Idempotent: a second run has nothing left to move, which is what makes a
	// migration that ran but failed to persist safe to repeat.
	if n := AdoptOrphanDevices(devices, "u1"); n != 0 {
		t.Errorf("a second run moved %d devices, want 0", n)
	}
}

// ListUser is how an account sees its own pairings and nobody else's.
func TestDeviceListUser(t *testing.T) {
	store := NewDeviceStore(nil)
	for _, spec := range []struct{ user, name string }{{"u1", "phone"}, {"u2", "laptop"}, {"u1", "tablet"}} {
		if _, _, err := store.Issue(spec.user, spec.name); err != nil {
			t.Fatalf("Issue: %v", err)
		}
	}

	got := store.ListUser("u1")
	if len(got) != 2 {
		t.Fatalf("ListUser(u1) returned %d devices, want 2", len(got))
	}
	for _, dev := range got {
		if dev.UserID != "u1" {
			t.Errorf("ListUser(u1) returned a device owned by %q", dev.UserID)
		}
	}
}

// The dirty flag is what keeps a per-request timestamp from turning into a
// per-request disk write.
func TestDeviceDirtyTracking(t *testing.T) {
	store := NewDeviceStore(nil)
	_, token, err := store.Issue("u1", "phone")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	store.Snapshot()
	if store.Dirty() {
		t.Fatal("still dirty right after a Snapshot")
	}
	if _, ok := store.Validate(token); !ok {
		t.Fatal("Validate failed")
	}
	if !store.Dirty() {
		t.Error("a use did not mark the store dirty")
	}
	if store.Snapshot(); store.Dirty() {
		t.Error("Snapshot did not clear the dirty flag")
	}
}

func TestDeviceStoreSeeding(t *testing.T) {
	store := NewDeviceStore(nil)
	dev, token, err := store.Issue("u1", "phone")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	persisted := store.Snapshot()

	// What survives a restart is the persisted list, so the token has to still
	// work against a store rebuilt from it.
	reloaded := NewDeviceStore(persisted)
	got, ok := reloaded.Validate(token)
	if !ok {
		t.Fatal("the token stopped working after a reload")
	}
	if got.ID != dev.ID {
		t.Errorf("reloaded device ID is %q, want %q", got.ID, dev.ID)
	}
}

// panel.json is a file operators are invited to edit, so a half-written entry
// has to be dropped rather than served as a device nobody can match or revoke.
func TestDeviceStoreSkipsMalformedEntries(t *testing.T) {
	store := NewDeviceStore([]DeviceToken{
		{ID: "", Hash: "abc", Name: "no id"},
		{ID: "abc", Hash: "", Name: "no hash"},
		{ID: "ok", Hash: HashDeviceToken(devicePrefix + "deadbeef"), Name: "fine"},
	})
	got := store.List()
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("List returned %+v, want only the well-formed entry", got)
	}
}

func TestDeviceListIsOrdered(t *testing.T) {
	store := NewDeviceStore(nil)
	for _, name := range []string{"first", "second", "third"} {
		if _, _, err := store.Issue("u1", name); err != nil {
			t.Fatalf("Issue(%s): %v", name, err)
		}
	}

	first := store.List()
	second := store.List()
	if len(first) != 3 {
		t.Fatalf("List returned %d devices, want 3", len(first))
	}
	// Map iteration is randomised, so an unsorted List would shuffle between
	// calls and the device list would jump around in the UI.
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("List is not stable: position %d was %q then %q", i, first[i].ID, second[i].ID)
		}
	}
}

func TestCleanDeviceName(t *testing.T) {
	t.Run("accepts", func(t *testing.T) {
		got, err := CleanDeviceName("  Lans 的 Pixel  ")
		if err != nil {
			t.Fatalf("CleanDeviceName: %v", err)
		}
		if got != "Lans 的 Pixel" {
			t.Errorf("got %q, want the trimmed name", got)
		}
	})

	t.Run("rejects", func(t *testing.T) {
		cases := map[string]string{
			"empty":       "",
			"whitespace":  "   ",
			"control":     "phone\nname",
			"tab":         "phone\tname",
			"over length": strings.Repeat("x", maxDeviceNameLen+1),
		}
		for name, candidate := range cases {
			if _, err := CleanDeviceName(candidate); err == nil {
				t.Errorf("%s: accepted %q", name, candidate)
			}
		}
	})

	t.Run("counts runes not bytes", func(t *testing.T) {
		// A name at the limit in runes is well over it in bytes once it is not
		// ASCII; rejecting that would make the limit mean something different
		// for Chinese names than for English ones.
		if _, err := CleanDeviceName(strings.Repeat("手", maxDeviceNameLen)); err != nil {
			t.Errorf("rejected a name that is %d runes: %v", maxDeviceNameLen, err)
		}
	})
}

// omitempty does nothing for a time.Time — a struct is never "empty" to
// encoding/json — so a plain field would put "0001-01-01T00:00:00Z" in the
// panel.json operators are told they can read.
func TestUnusedDeviceOmitsLastUsed(t *testing.T) {
	store := NewDeviceStore(nil)
	if _, _, err := store.Issue("u1", "phone"); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	encoded, err := json.Marshal(store.Snapshot())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "lastUsed") {
		t.Errorf("a never-used device serialised a lastUsed field: %s", encoded)
	}
	if strings.Contains(string(encoded), "0001-01-01") {
		t.Errorf("the zero time reached the JSON: %s", encoded)
	}
}

func TestIssueRejectsBadName(t *testing.T) {
	store := NewDeviceStore(nil)
	if _, _, err := store.Issue("u1", "  "); err == nil {
		t.Fatal("Issue accepted a blank name")
	}
	if got := store.List(); len(got) != 0 {
		t.Errorf("a rejected Issue still added %d devices", len(got))
	}
}
