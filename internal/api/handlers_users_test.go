package api

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/users"
)

const memberPass = "member-password-1"

// asMember signs in as a freshly created account holding role, on a client of
// its own so the administrator's cookie is not disturbed.
//
// It goes through the real login endpoint rather than reaching into the session
// store, because what these tests are about is the path a request actually
// takes.
func (e *testEnv) asMember(username, role string) *testEnv {
	e.t.Helper()

	if _, err := e.accounts.AddUser(username, "", role, memberPass); err != nil {
		e.t.Fatalf("AddUser(%s): %v", username, err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		e.t.Fatalf("cookiejar: %v", err)
	}
	member := *e
	member.client = &http.Client{Jar: jar}

	resp := member.do(http.MethodPost, "/api/auth/login", loginRequest{Username: username, Password: memberPass})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("member login failed: %d", resp.StatusCode)
	}
	return &member
}

func (e *testEnv) status(method, path string, body any) int {
	e.t.Helper()
	resp := e.do(method, path, body)
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestCapabilitiesAreEnforced is the point of the whole thing: a role that does
// not name a capability must not reach the routes requiring it.
func TestCapabilitiesAreEnforced(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	ops := env.asMember("ops", users.RoleOps)

	// What 运维 is for.
	for _, path := range []string{"/api/instances", "/api/system"} {
		if got := ops.status(http.MethodGet, path, nil); got != http.StatusOK {
			t.Errorf("GET %s as 运维: %d, want 200", path, got)
		}
	}

	// What it exists not to reach. The host shell and the host filesystem are
	// the two that turn the panel password into a shell account; the rest would
	// let the role grant itself the first two.
	for _, path := range []string{
		"/api/terminal", "/api/fs", "/api/users", "/api/roles",
		"/api/capabilities", "/api/update", "/api/databases", "/api/plugins",
	} {
		if got := ops.status(http.MethodGet, path, nil); got != http.StatusForbidden {
			t.Errorf("GET %s as 运维: %d, want 403", path, got)
		}
	}
}

// The administrator is not a special case in the middleware — they simply hold
// everything — so this checks the enforcement did not break the normal path.
func TestAdminReachesEverything(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	for _, path := range []string{"/api/users", "/api/roles", "/api/capabilities", "/api/terminal", "/api/fs?dir=/"} {
		if got := env.status(http.MethodGet, path, nil); got != http.StatusOK {
			t.Errorf("GET %s as administrator: %d, want 200", path, got)
		}
	}
}

// Routes marked CapSignedIn belong to everybody: an account with the emptiest
// possible role still gets to see who it is and sign out.
func TestSignedInRoutesNeedNoCapability(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	empty, err := env.accounts.AddRole("空角色", nil)
	if err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	member := env.asMember("nobody", empty.ID)

	resp := member.do(http.MethodGet, "/api/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("GET /api/auth/me: %d, want 200", resp.StatusCode)
	}
	var me userResponse
	decodeBody(t, resp, &me)
	if me.Username != "nobody" {
		t.Errorf("/api/auth/me says %q", me.Username)
	}
	if len(me.Capabilities) != 0 {
		t.Errorf("an empty role reported %v", me.Capabilities)
	}
	if got := member.status(http.MethodGet, "/api/auth/devices", nil); got != http.StatusOK {
		t.Errorf("GET /api/auth/devices with an empty role: %d, want 200", got)
	}
	// But it reaches nothing else at all.
	if got := member.status(http.MethodGet, "/api/instances", nil); got != http.StatusForbidden {
		t.Errorf("GET /api/instances with an empty role: %d, want 403", got)
	}
}

// /api/auth/me is what the browser hides buttons from, so it has to report the
// resolved set rather than the role name.
func TestMeReportsCapabilities(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodGet, "/api/auth/me", nil)
	var me userResponse
	decodeBody(t, resp, &me)
	if me.RoleID != users.RoleAdmin {
		t.Errorf("role is %q, want %q", me.RoleID, users.RoleAdmin)
	}
	if len(me.Capabilities) != len(authz.All()) {
		t.Errorf("administrator reported %d capabilities, want the whole vocabulary (%d)",
			len(me.Capabilities), len(authz.All()))
	}
}

// Editing a role must reach the accounts holding it on their next request,
// without them signing in again: capabilities are resolved per request.
func TestRoleEditsTakeEffectImmediately(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	ops := env.asMember("ops", users.RoleOps)

	if got := ops.status(http.MethodGet, "/api/java", nil); got != http.StatusForbidden {
		t.Fatalf("GET /api/java as 运维: %d, want 403", got)
	}
	if got := env.status(http.MethodPut, "/api/roles/"+users.RoleOps, roleRequest{
		Name:         "运维",
		Capabilities: []authz.Cap{authz.CapInstanceView, authz.CapPanelJava},
	}); got != http.StatusOK {
		t.Fatalf("PUT /api/roles/ops: %d, want 200", got)
	}
	if got := ops.status(http.MethodGet, "/api/java", nil); got != http.StatusOK {
		t.Errorf("GET /api/java after the role was given it: %d, want 200", got)
	}
	if got := ops.status(http.MethodGet, "/api/system", nil); got != http.StatusForbidden {
		t.Errorf("GET /api/system after the role lost it: %d, want 403", got)
	}
}

// Disabling or deleting an account has to lock it out now, not when its session
// happens to expire. This is why requireAuth re-reads the account every request.
func TestDisablingAnAccountEndsItsSessionAtOnce(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	ops := env.asMember("ops", users.RoleOps)
	if got := ops.status(http.MethodGet, "/api/instances", nil); got != http.StatusOK {
		t.Fatalf("the member could not reach anything to begin with: %d", got)
	}

	var accounts []accountResponse
	decodeBody(t, env.do(http.MethodGet, "/api/users", nil), &accounts)
	var id string
	for _, a := range accounts {
		if a.Username == "ops" {
			id = a.ID
		}
	}
	if id == "" {
		t.Fatal("the new account is not in /api/users")
	}

	if got := env.status(http.MethodPut, "/api/users/"+id, updateUserRequest{
		Username: "ops", RoleID: users.RoleOps, Disabled: true,
	}); got != http.StatusOK {
		t.Fatalf("disabling the account: %d, want 200", got)
	}
	if got := ops.status(http.MethodGet, "/api/instances", nil); got != http.StatusUnauthorized {
		t.Errorf("a disabled account still reached the panel: %d, want 401", got)
	}

	// And deleting one does the same.
	if got := env.status(http.MethodPut, "/api/users/"+id, updateUserRequest{
		Username: "ops", RoleID: users.RoleOps,
	}); got != http.StatusOK {
		t.Fatalf("re-enabling the account: %d", got)
	}
	ops = env.asMemberExisting("ops")
	if got := env.status(http.MethodDelete, "/api/users/"+id, nil); got != http.StatusNoContent {
		t.Fatalf("deleting the account: %d, want 204", got)
	}
	if got := ops.status(http.MethodGet, "/api/instances", nil); got != http.StatusUnauthorized {
		t.Errorf("a deleted account still reached the panel: %d, want 401", got)
	}
}

// TestDisabledAccountIsRefusedByRequireAuth isolates the actual barrier.
//
// The disable handler also drops the account's sessions, which would make
// TestDisablingAnAccountEndsItsSessionAtOnce pass even if requireAuth trusted
// the session — so this one disables the account in the registry directly,
// leaving the session alive, and checks the request is refused anyway. That is
// the property the design rests on: the account is re-read every request, so a
// credential outliving its owner is worth nothing.
func TestDisabledAccountIsRefusedByRequireAuth(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	ops := env.asMember("ops", users.RoleOps)
	if got := ops.status(http.MethodGet, "/api/instances", nil); got != http.StatusOK {
		t.Fatalf("the member could not reach anything to begin with: %d", got)
	}

	account, ok := env.accounts.ByUsername("ops")
	if !ok {
		t.Fatal("the account is missing from the registry")
	}
	if _, err := env.accounts.UpdateUser(account.ID, users.Edit{
		Username: "ops", RoleID: users.RoleOps, Disabled: true,
	}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	if got := ops.status(http.MethodGet, "/api/instances", nil); got != http.StatusUnauthorized {
		t.Errorf("a live session for a disabled account was honoured: %d, want 401", got)
	}
}

// The same barrier on the other credential. A paired phone has no session to
// revoke, so requireAuth re-reading the account is the only thing standing
// between a disabled account and a token that never expires.
func TestDisabledAccountIsRefusedOnItsDeviceToken(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	member := env.asMember("ops", users.RoleOps)

	paired := member.pairAs("ops", memberPass, "phone")
	if got := member.bearer(http.MethodGet, "/api/instances", paired.Token, nil); got.StatusCode != http.StatusOK {
		got.Body.Close()
		t.Fatalf("the fresh pairing could not reach anything: %d", got.StatusCode)
	}

	account, _ := env.accounts.ByUsername("ops")
	if _, err := env.accounts.UpdateUser(account.ID, users.Edit{
		Username: "ops", RoleID: users.RoleOps, Disabled: true,
	}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	resp := member.bearer(http.MethodGet, "/api/instances", paired.Token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a device token outlived its disabled account: %d, want 401", resp.StatusCode)
	}
}

// A pairing inherits the account's role, so it is another way in rather than a
// way round. It also belongs to that account alone: the device list is "my
// devices", not the panel's.
func TestPairingBelongsToItsAccount(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	member := env.asMember("ops", users.RoleOps)

	adminDevice := env.pair("admin laptop")
	memberDevice := member.pairAs("ops", memberPass, "member phone")

	// The token carries 运维's capabilities, not the administrator's.
	resp := member.bearer(http.MethodGet, "/api/terminal", memberDevice.Token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a 运维 device token reached the host shell: %d, want 403", resp.StatusCode)
	}

	// Each account sees only its own pairing.
	for _, tc := range []struct {
		env  *testEnv
		want string
	}{{env, "admin laptop"}, {member, "member phone"}} {
		var listed []deviceResponse
		decodeBody(t, tc.env.do(http.MethodGet, "/api/auth/devices", nil), &listed)
		if len(listed) != 1 || listed[0].Name != tc.want {
			t.Errorf("device list is %+v, want only %q", listed, tc.want)
		}
	}

	// And cannot revoke the other's.
	if got := member.status(http.MethodDelete, "/api/auth/devices/"+adminDevice.ID, nil); got != http.StatusNotFound {
		t.Errorf("revoking somebody else's device: %d, want 404", got)
	}
}

// asMemberExisting signs in as an account that is already there.
func (e *testEnv) asMemberExisting(username string) *testEnv {
	e.t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		e.t.Fatalf("cookiejar: %v", err)
	}
	member := *e
	member.client = &http.Client{Jar: jar}

	resp := member.do(http.MethodPost, "/api/auth/login", loginRequest{Username: username, Password: memberPass})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("member login failed: %d", resp.StatusCode)
	}
	return &member
}

// One person changing their password must not sign the whole team out. It used
// to revoke everything, which was right when there was only ever one account.
func TestPasswordChangeOnlyAffectsItsOwnAccount(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	ops := env.asMember("ops", users.RoleOps)

	if got := ops.status(http.MethodPost, "/api/auth/password", changePasswordRequest{
		CurrentPassword: memberPass, NewPassword: "a-new-password-2",
	}); got != http.StatusNoContent {
		t.Fatalf("changing the member's password: %d, want 204", got)
	}

	// The member's own session is gone...
	if got := ops.status(http.MethodGet, "/api/instances", nil); got != http.StatusUnauthorized {
		t.Errorf("the session survived its own password change: %d, want 401", got)
	}
	// ...and the administrator is still signed in.
	if got := env.status(http.MethodGet, "/api/users", nil); got != http.StatusOK {
		t.Errorf("the administrator was signed out by somebody else's password change: %d", got)
	}
}

// The last administrator is the invariant the API has to hold as firmly as the
// registry does, since this is the path an operator actually takes.
func TestAPIRefusesToRemoveTheLastAdmin(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	var accounts []accountResponse
	decodeBody(t, env.do(http.MethodGet, "/api/users", nil), &accounts)
	if len(accounts) != 1 || !accounts[0].Self {
		t.Fatalf("expected one account, the caller's own: %+v", accounts)
	}
	id := accounts[0].ID

	if got := env.status(http.MethodDelete, "/api/users/"+id, nil); got != http.StatusConflict {
		t.Errorf("deleting the last administrator: %d, want 409", got)
	}
	if got := env.status(http.MethodPut, "/api/users/"+id, updateUserRequest{
		Username: accounts[0].Username, RoleID: users.RoleOps,
	}); got != http.StatusConflict {
		t.Errorf("demoting the last administrator: %d, want 409", got)
	}
	// And the panel is still reachable afterwards.
	if got := env.status(http.MethodGet, "/api/users", nil); got != http.StatusOK {
		t.Errorf("the administrator lost access anyway: %d", got)
	}
}

// Accounts and roles must never carry anything derived from a password.
func TestAccountListingCarriesNoCredential(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.asMember("ops", users.RoleOps)

	resp := env.do(http.MethodGet, "/api/users", nil)
	body := readAll(t, resp)
	for _, secret := range []string{"credential", "hash", "salt", "iterations", memberPass} {
		if strings.Contains(body, secret) {
			t.Errorf("GET /api/users leaked %q:\n%s", secret, body)
		}
	}
}

func TestCreateUserValidates(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	for _, tc := range []struct {
		name string
		req  createUserRequest
		want int
	}{
		{"blank username", createUserRequest{Username: " ", RoleID: users.RoleOps, Password: memberPass}, http.StatusBadRequest},
		{"unknown role", createUserRequest{Username: "x", RoleID: "nope", Password: memberPass}, http.StatusBadRequest},
		{"short password", createUserRequest{Username: "x", RoleID: users.RoleOps, Password: "short"}, http.StatusBadRequest},
		{"good", createUserRequest{Username: "x", RoleID: users.RoleOps, Password: memberPass}, http.StatusCreated},
		{"duplicate", createUserRequest{Username: "X", RoleID: users.RoleOps, Password: memberPass}, http.StatusConflict},
	} {
		if got := env.status(http.MethodPost, "/api/users", tc.req); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Accounts survive a restart, which is the whole reason users.json exists.
func TestAccountsRoundTripThroughDisk(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	if got := env.status(http.MethodPost, "/api/users", createUserRequest{
		Username: "ops", RoleID: users.RoleOps, Password: memberPass,
	}); got != http.StatusCreated {
		t.Fatalf("creating the account: %d", got)
	}

	file, existed, err := env.store.LoadUsers()
	if err != nil {
		t.Fatalf("LoadUsers: %v", err)
	}
	if !existed {
		t.Fatal("users.json was not written")
	}
	reloaded, complaints, err := users.New(file)
	if err != nil {
		t.Fatalf("users.New: %v", err)
	}
	if len(complaints) > 0 {
		t.Errorf("reloading what was just written complained: %v", complaints)
	}
	if _, err := reloaded.Authenticate("ops", memberPass); err != nil {
		t.Errorf("the account could not sign in after a reload: %v", err)
	}
}
