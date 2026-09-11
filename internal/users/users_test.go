package users

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/authz"
)

const testPassword = "correct-horse-battery"

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()

	cred, err := auth.NewCredential("carlos", testPassword)
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	file, err := Initial(cred)
	if err != nil {
		t.Fatalf("Initial: %v", err)
	}
	r, complaints, err := New(file)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(complaints) > 0 {
		t.Fatalf("New complained about a file it just built: %v", complaints)
	}
	return r
}

func TestInitialMakesOneAdminAndThePresets(t *testing.T) {
	r := newTestRegistry(t)

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("got %d accounts, want 1", len(list))
	}
	if list[0].RoleID != RoleAdmin {
		t.Errorf("the migrated operator holds %q, want %q", list[0].RoleID, RoleAdmin)
	}
	if list[0].Username != "carlos" {
		t.Errorf("username is %q, want carlos", list[0].Username)
	}

	var ids []string
	for _, role := range r.Roles() {
		ids = append(ids, role.ID)
	}
	if !slices.Equal(ids, []string{RoleAdmin, RoleOps, RoleDev}) {
		t.Errorf("roles are %v, want the built-in one plus the two presets", ids)
	}
}

// The administrator's capabilities are synthesised rather than stored, so a
// capability added in a later release is one they already have.
func TestAdminHoldsTheWholeVocabulary(t *testing.T) {
	r := newTestRegistry(t)
	admin, _ := r.FirstAdmin()

	caps := r.Capabilities(admin)
	for _, info := range authz.All() {
		if !caps[info.Cap] {
			t.Errorf("the administrator does not hold %q", info.Cap)
		}
	}
}

func TestPresetRolesHoldWhatTheyClaim(t *testing.T) {
	r := newTestRegistry(t)

	ops, err := r.AddUser("ops", "", RoleOps, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	caps := r.Capabilities(ops)

	for _, want := range []authz.Cap{authz.CapInstanceView, authz.CapInstancePower, authz.CapInstanceConsole} {
		if !caps[want] {
			t.Errorf("运维 does not hold %q", want)
		}
	}
	// The point of the role: it stops at the boundary of the host.
	for _, never := range []authz.Cap{
		authz.CapPanelTerminal, authz.CapPanelHostFS, authz.CapInstanceFilesWrite,
		authz.CapInstancePlugins, authz.CapInstanceLaunch, authz.CapPanelUsers,
	} {
		if caps[never] {
			t.Errorf("运维 holds %q, which is the whole thing the role is meant not to have", never)
		}
	}

	dev, err := r.AddUser("dev", "", RoleDev, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	devCaps := r.Capabilities(dev)
	// 开发 is deliberately not a security boundary — it decides what code a
	// server runs — but it still must not reach the panel's own controls.
	for _, want := range []authz.Cap{authz.CapInstancePlugins, authz.CapInstanceLaunch, authz.CapLibraryPlugins} {
		if !devCaps[want] {
			t.Errorf("开发 does not hold %q", want)
		}
	}
	for _, never := range []authz.Cap{authz.CapPanelTerminal, authz.CapPanelUsers, authz.CapPanelUpdate, authz.CapPanelDatabases} {
		if devCaps[never] {
			t.Errorf("开发 holds %q", never)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	r := newTestRegistry(t)

	if _, err := r.Authenticate("carlos", testPassword); err != nil {
		t.Errorf("the right password was refused: %v", err)
	}
	// Case-insensitive lookup: the name is an identifier, not a secret.
	if _, err := r.Authenticate("CARLOS", testPassword); err != nil {
		t.Errorf("a differently-cased username was refused: %v", err)
	}
	for _, tc := range []struct{ name, user, pass string }{
		{"wrong password", "carlos", "nope"},
		{"unknown account", "nobody", testPassword},
		{"empty", "", ""},
	} {
		if _, err := r.Authenticate(tc.user, tc.pass); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

func TestAuthenticateRefusesADisabledAccount(t *testing.T) {
	r := newTestRegistry(t)
	u, err := r.AddUser("ops", "", RoleOps, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if _, err := r.Authenticate("ops", testPassword); err != nil {
		t.Fatalf("the account could not sign in before being disabled: %v", err)
	}

	if _, err := r.UpdateUser(u.ID, Edit{Username: "ops", RoleID: RoleOps, Disabled: true}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if _, err := r.Authenticate("ops", testPassword); err == nil {
		t.Error("a disabled account signed in")
	}
}

// An unknown username must cost the same as a known one. The check is a
// generous lower bound rather than a comparison: what it catches is the
// regression where the lookup misses and Authenticate returns immediately,
// which would let anyone map the panel's account names off the clock.
func TestAuthenticateSpendsADerivationOnAnUnknownAccount(t *testing.T) {
	r := newTestRegistry(t)

	start := time.Now()
	if _, err := r.Authenticate("nobody-at-all", testPassword); err == nil {
		t.Fatal("an unknown account was accepted")
	}
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Errorf("refusing an unknown account took %v, which is too fast to have "+
			"derived anything — the decoy credential is not being used", elapsed)
	}
}

func TestUsernamesAreUnique(t *testing.T) {
	r := newTestRegistry(t)
	if _, err := r.AddUser("ops", "", RoleOps, testPassword, nil); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if _, err := r.AddUser("OPS", "", RoleOps, testPassword, nil); err == nil {
		t.Error("a username differing only in case was accepted")
	}
}

func TestAddUserRejectsBadInput(t *testing.T) {
	r := newTestRegistry(t)
	for _, tc := range []struct{ name, user, role, pass string }{
		{"blank username", "  ", RoleOps, testPassword},
		{"username with a space", "two words", RoleOps, testPassword},
		{"unknown role", "someone", "nope", testPassword},
		{"short password", "someone", RoleOps, "short"},
	} {
		if _, err := r.AddUser(tc.user, "", tc.role, tc.pass, nil); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

// The invariant that makes every other edit safe to allow: a panel with no way
// in cannot be fixed from inside the panel.
func TestTheLastAdminCannotBeRemoved(t *testing.T) {
	r := newTestRegistry(t)
	admin, _ := r.FirstAdmin()

	if _, err := r.DeleteUser(admin.ID); err == nil {
		t.Error("the last administrator was deleted")
	}
	if _, err := r.UpdateUser(admin.ID, Edit{Username: admin.Username, RoleID: RoleOps}); err == nil {
		t.Error("the last administrator was demoted")
	}
	if _, err := r.UpdateUser(admin.ID, Edit{Username: admin.Username, RoleID: RoleAdmin, Disabled: true}); err == nil {
		t.Error("the last administrator was disabled")
	}

	// With a second one in place, all three become ordinary edits.
	second, err := r.AddUser("second", "", RoleAdmin, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if _, err := r.UpdateUser(admin.ID, Edit{Username: admin.Username, RoleID: RoleOps}); err != nil {
		t.Errorf("demoting one of two administrators was refused: %v", err)
	}
	// And the invariant moves with them: the survivor is now the last one.
	if _, err := r.DeleteUser(second.ID); err == nil {
		t.Error("the administrator that was left was deleted")
	}
}

// A disabled administrator does not count towards the one that has to remain:
// an account nobody can sign in to is not a way back in.
func TestADisabledAdminDoesNotCount(t *testing.T) {
	r := newTestRegistry(t)
	admin, _ := r.FirstAdmin()

	spare, err := r.AddUser("spare", "", RoleAdmin, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if _, err := r.UpdateUser(spare.ID, Edit{Username: "spare", RoleID: RoleAdmin, Disabled: true}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if _, err := r.DeleteUser(admin.ID); err == nil {
		t.Error("the only enabled administrator was deleted while a disabled one existed")
	}
}

func TestRoles(t *testing.T) {
	r := newTestRegistry(t)

	role, err := r.AddRole("建筑师", []authz.Cap{authz.CapInstanceView, authz.CapInstanceSchematics})
	if err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	if _, err := r.AddRole("坏的", []authz.Cap{"instance:nope"}); err == nil {
		t.Error("a role naming a capability outside the vocabulary was accepted")
	}
	if _, err := r.UpdateRole(RoleAdmin, "改名", nil); err == nil {
		t.Error("the built-in administrator role was edited")
	}
	if _, err := r.DeleteRole(RoleAdmin); err == nil {
		t.Error("the built-in administrator role was deleted")
	}

	// A role in use cannot be deleted: the account holding it would be left
	// with nothing, which reads as a permission bug rather than a deletion.
	u, err := r.AddUser("builder", "", role.ID, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if _, err := r.DeleteRole(role.ID); err == nil {
		t.Error("a role still held by an account was deleted")
	}
	if _, err := r.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := r.DeleteRole(role.ID); err != nil {
		t.Errorf("an unused role could not be deleted: %v", err)
	}
}

// Editing a role reaches every account holding it at once. That is the whole
// reason roles exist rather than per-account capability lists.
func TestEditingARoleReachesItsAccounts(t *testing.T) {
	r := newTestRegistry(t)
	u, err := r.AddUser("ops", "", RoleOps, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if r.Capabilities(u)[authz.CapPanelJava] {
		t.Fatal("运维 already holds the Java capability; pick another for this test")
	}

	if _, err := r.UpdateRole(RoleOps, "运维", []authz.Cap{authz.CapPanelJava}); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	caps := r.Capabilities(u)
	if !caps[authz.CapPanelJava] {
		t.Error("the account did not gain the capability its role was given")
	}
	if caps[authz.CapInstancePower] {
		t.Error("the account kept a capability its role no longer has")
	}
}

// users.json is a file operators are invited to edit, so New repairs rather
// than refuses — but never in a direction that hands out more than was written.
func TestNewDropsBrokenRowsRatherThanRefusing(t *testing.T) {
	good, err := auth.NewCredential("good", testPassword)
	if err != nil {
		t.Fatalf("NewCredential: %v", err)
	}
	file := File{
		Users: []User{
			{ID: "1", Username: "good", RoleID: RoleAdmin, Credential: good},
			{ID: "", Username: "no-id", RoleID: RoleAdmin, Credential: good},
			{ID: "3", Username: "no-password", RoleID: RoleAdmin},
			{ID: "4", Username: "good", RoleID: RoleAdmin, Credential: good},
			{ID: "5", Username: "ghost-role", RoleID: "vanished", Credential: good},
		},
		Roles: []Role{
			{ID: "r1", Name: "有一条不认识的能力", Caps: []authz.Cap{authz.CapInstanceView, "instance:fly"}},
			{ID: RoleAdmin, Name: "冒充内置角色", Caps: []authz.Cap{authz.CapPanelUsers}},
		},
	}

	r, complaints, err := New(file)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// One per dropped row: four bad accounts, the stored role claiming the
	// built-in id, and the unknown capability inside the role that was kept.
	if len(complaints) != 6 {
		t.Errorf("got %d complaints, want 6: %v", len(complaints), complaints)
	}
	if got := r.List(); len(got) != 1 || got[0].ID != "1" {
		t.Errorf("kept %v, want only the sound row", got)
	}

	// An account pointing at a role that is not there is dropped rather than
	// given a fallback: every fallback is a guess about what someone may do.
	if _, ok := r.ByUsername("ghost-role"); ok {
		t.Error("an account whose role does not exist was loaded")
	}
	// The built-in role cannot be shadowed by a stored one claiming its id.
	admin, ok := r.ByUsername("good")
	if !ok {
		t.Fatal("the sound row is missing")
	}
	if caps := r.Capabilities(admin); len(caps) != len(authz.All()) {
		t.Error("a stored role claiming the admin id replaced the built-in one")
	}
	// The unknown capability is dropped, the known one survives.
	for _, role := range r.Roles() {
		if role.ID != "r1" {
			continue
		}
		if !slices.Equal(role.Caps, []authz.Cap{authz.CapInstanceView}) {
			t.Errorf("role kept %v, want only the capability this build knows", role.Caps)
		}
	}
}

func TestCleanUsername(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"  carlos  ", "carlos", true},
		{"a.b-c_1", "a.b-c_1", true},
		{"", "", false},
		{"two words", "", false},
		{"名字", "", false},
		{strings.Repeat("a", maxNameLen+1), "", false},
	} {
		got, err := CleanUsername(tc.in)
		if tc.ok && err != nil {
			t.Errorf("CleanUsername(%q) failed: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("CleanUsername(%q) was accepted", tc.in)
		}
		if tc.ok && got != tc.want {
			t.Errorf("CleanUsername(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Renaming must not break the password, which is derived under the old name.
func TestRenameKeepsThePasswordWorking(t *testing.T) {
	r := newTestRegistry(t)
	admin, _ := r.FirstAdmin()

	if _, err := r.UpdateUser(admin.ID, Edit{Username: "lans", RoleID: RoleAdmin}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if _, err := r.Authenticate("lans", testPassword); err != nil {
		t.Errorf("the password stopped working after a rename: %v", err)
	}
	if _, err := r.Authenticate("carlos", testPassword); err == nil {
		t.Error("the old username still signs in")
	}
}

func TestInstanceGrant(t *testing.T) {
	r := newTestRegistry(t)

	// nil is "every server", which is what every account had before grants
	// existed and what an omitted field decodes to.
	open, err := r.AddUser("open", "", RoleOps, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if !r.CanUseInstance(open, "anything") {
		t.Error("an account with no grant set was refused a server")
	}

	// An empty list is the opposite, and the two must stay distinguishable.
	none, err := r.AddUser("none", "", RoleOps, testPassword, []string{})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if r.CanUseInstance(none, "anything") {
		t.Error("an empty grant reached a server")
	}

	some, err := r.AddUser("some", "", RoleOps, testPassword, []string{"a", "b"})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if !r.CanUseInstance(some, "a") || r.CanUseInstance(some, "c") {
		t.Errorf("grant %v resolved wrongly", some.Instances)
	}
}

// An administrator's grant is forced to nil wherever it is written.
func TestAdminGrantIsAlwaysEverything(t *testing.T) {
	r := newTestRegistry(t)

	u, err := r.AddUser("second", "", RoleAdmin, testPassword, []string{"a"})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if u.Instances != nil {
		t.Errorf("a new administrator was created with grant %v", u.Instances)
	}
	updated, err := r.UpdateUser(u.ID, Edit{Username: "second", RoleID: RoleAdmin, Instances: []string{"a"}})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.Instances != nil {
		t.Errorf("an administrator was updated to grant %v", updated.Instances)
	}

	// Demoting them keeps whatever grant the edit carried, because now it means
	// something.
	demoted, err := r.UpdateUser(u.ID, Edit{Username: "second", RoleID: RoleOps, Instances: []string{"a"}})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if len(demoted.Instances) != 1 || demoted.Instances[0] != "a" {
		t.Errorf("a demoted administrator got grant %v, want [a]", demoted.Instances)
	}
}

func TestGrantInstanceAndForgetInstance(t *testing.T) {
	r := newTestRegistry(t)
	limited, err := r.AddUser("limited", "", RoleOps, testPassword, []string{"a"})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	open, err := r.AddUser("open", "", RoleOps, testPassword, nil)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	granted, err := r.GrantInstance(limited.ID, "b")
	if err != nil || !granted {
		t.Fatalf("GrantInstance: %v (granted=%v)", err, granted)
	}
	// Twice is a no-op rather than a duplicate.
	if again, _ := r.GrantInstance(limited.ID, "b"); again {
		t.Error("granting the same server twice reported a change")
	}
	// And an account that already reaches everything needs nothing added.
	if granted, _ := r.GrantInstance(open.ID, "b"); granted {
		t.Error("an account with no grant set was given an explicit one")
	}

	if n := r.ForgetInstance("b"); n != 1 {
		t.Errorf("ForgetInstance touched %d accounts, want 1", n)
	}
	reloaded, _ := r.ByID(limited.ID)
	if len(reloaded.Instances) != 1 || reloaded.Instances[0] != "a" {
		t.Errorf("grant is %v after forgetting b, want [a]", reloaded.Instances)
	}
	// The account that reaches everything is untouched: nil is not a list with
	// an entry to remove.
	if reloadedOpen, _ := r.ByID(open.ID); reloadedOpen.Instances != nil {
		t.Errorf("an unrestricted grant became %v", reloadedOpen.Instances)
	}
}

// The grant survives a write and a read, including the nil/empty distinction
// that everything else rests on.
func TestGrantRoundTripsThroughTheFile(t *testing.T) {
	r := newTestRegistry(t)
	if _, err := r.AddUser("none", "", RoleOps, testPassword, []string{}); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if _, err := r.AddUser("all", "", RoleOps, testPassword, nil); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	encoded, err := json.Marshal(r.Snapshot())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var file File
	if err := json.Unmarshal(encoded, &file); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	reloaded, complaints, err := New(file)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(complaints) > 0 {
		t.Fatalf("reloading complained: %v", complaints)
	}

	none, _ := reloaded.ByUsername("none")
	if reloaded.CanUseInstance(none, "a") {
		t.Error("an empty grant came back as unrestricted after a round trip")
	}
	all, _ := reloaded.ByUsername("all")
	if !reloaded.CanUseInstance(all, "a") {
		t.Error("an unrestricted grant came back as empty after a round trip")
	}
}
