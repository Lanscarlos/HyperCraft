package auth

import (
	"strings"
	"testing"
)

func TestDeviceIssueAndValidate(t *testing.T) {
	store := NewDeviceStore(nil)

	dev, token, err := store.Issue("Lans 的手机")
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
	if got.LastUsed.IsZero() {
		t.Error("Validate did not record the use")
	}
}

func TestDeviceValidateRejects(t *testing.T) {
	store := NewDeviceStore(nil)
	dev, token, err := store.Issue("phone")
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
	dev, token, err := store.Issue("phone")
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
		_, token, err := store.Issue(name)
		if err != nil {
			t.Fatalf("Issue(%s): %v", name, err)
		}
		tokens = append(tokens, token)
	}

	if n := store.RevokeAll(); n != 3 {
		t.Errorf("RevokeAll reported %d devices, want 3", n)
	}
	for _, token := range tokens {
		if _, ok := store.Validate(token); ok {
			t.Error("a token survived RevokeAll")
		}
	}
	if got := store.List(); len(got) != 0 {
		t.Errorf("List returned %d devices after RevokeAll", len(got))
	}
}

// The dirty flag is what keeps a per-request timestamp from turning into a
// per-request disk write.
func TestDeviceDirtyTracking(t *testing.T) {
	store := NewDeviceStore(nil)
	_, token, err := store.Issue("phone")
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
	dev, token, err := store.Issue("phone")
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
		if _, _, err := store.Issue(name); err != nil {
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

func TestIssueRejectsBadName(t *testing.T) {
	store := NewDeviceStore(nil)
	if _, _, err := store.Issue("  "); err == nil {
		t.Fatal("Issue accepted a blank name")
	}
	if got := store.List(); len(got) != 0 {
		t.Errorf("a rejected Issue still added %d devices", len(got))
	}
}
