package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/auth"
)

// pair mints a device token the way a native client does: by presenting the
// password, with no session and no CSRF header involved.
func (e *testEnv) pair(name string) createDeviceResponse {
	e.t.Helper()

	body, err := json.Marshal(createDeviceRequest{Username: testUser, Password: testPass, Name: name})
	if err != nil {
		e.t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(e.server.URL+"/api/auth/devices", "application/json", bytes.NewReader(body))
	if err != nil {
		e.t.Fatalf("pair %q: %v", name, err)
	}
	if resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		e.t.Fatalf("pair %q: status %d", name, resp.StatusCode)
	}

	var out createDeviceResponse
	decodeBody(e.t, resp, &out)
	return out
}

// bearer issues a request carrying a device token, on a client with no cookie
// jar so nothing but the token can be authenticating it, and with no CSRF
// header so the bearer exemption is exercised too.
func (e *testEnv) bearer(method, path, token string, body any) *http.Response {
	e.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func TestPairDeviceRequiresPassword(t *testing.T) {
	env := newTestEnv(t)

	// Being signed in in the browser is explicitly not enough: minting a
	// credential that outlives every session takes the credential itself.
	env.login()
	resp := env.do(http.MethodPost, "/api/auth/devices", createDeviceRequest{
		Username: testUser, Password: "wrong-password", Name: "phone",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pairing with a bad password returned %d, want 401", resp.StatusCode)
	}

	if devices := env.storedDevices(t); len(devices) != 0 {
		t.Errorf("a failed pairing persisted %d devices", len(devices))
	}
}

func TestPairDeviceRejectsBadName(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(http.MethodPost, "/api/auth/devices", createDeviceRequest{
		Username: testUser, Password: testPass, Name: "  ",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pairing with a blank name returned %d, want 400", resp.StatusCode)
	}
}

func TestDeviceTokenAuthenticates(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("Lans 的手机")

	if device.Token == "" {
		t.Fatal("pairing returned no token")
	}

	resp := env.bearer(http.MethodGet, "/api/auth/me", device.Token, nil)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("GET /api/auth/me with a device token returned %d", resp.StatusCode)
	}
	var me userResponse
	decodeBody(t, resp, &me)
	if me.Username != testUser {
		t.Errorf("username is %q, want %q", me.Username, testUser)
	}
	// The app has somewhere to show which pairing it is running under.
	if me.Device != "Lans 的手机" {
		t.Errorf("device name is %q, want the paired name", me.Device)
	}
}

// A native client cannot be expected to send the CSRF header, and does not need
// to: the browser never attaches an Authorization header on its own.
func TestDeviceTokenExemptFromCSRF(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("phone")

	resp := env.bearer(http.MethodPost, "/api/instances", device.Token, instanceRequest{
		Name: "csrf-free", Java: "java", Jar: "server.jar",
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("a bearer request was rejected for a missing CSRF header")
	}
	// Asserting the write actually landed, rather than just "not 403", keeps
	// the test honest: a 401 would otherwise pass it too.
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("creating an instance over bearer returned %d, want 201", resp.StatusCode)
	}
}

// The two credential kinds are deliberately not interchangeable.
func TestSessionTokenIsNotABearerCredential(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	base, err := url.Parse(env.server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	var sessionToken string
	for _, cookie := range env.client.Jar.Cookies(base) {
		if cookie.Name == sessionCookie {
			sessionToken = cookie.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("no session cookie after login")
	}

	resp := env.bearer(http.MethodGet, "/api/auth/me", sessionToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a session token was accepted as a bearer credential (%d)", resp.StatusCode)
	}
}

func TestBearerRejectsGarbage(t *testing.T) {
	env := newTestEnv(t)
	env.pair("phone")

	for _, token := range []string{"nonsense", "hcd_", "hcd_00000000"} {
		resp := env.bearer(http.MethodGet, "/api/auth/me", token, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q returned %d, want 401", token, resp.StatusCode)
		}
	}
}

func TestListDevices(t *testing.T) {
	env := newTestEnv(t)
	phone := env.pair("phone")
	env.pair("tablet")

	resp := env.bearer(http.MethodGet, "/api/auth/devices", phone.Token, nil)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		t.Fatalf("listing devices returned %d", resp.StatusCode)
	}

	var listed []deviceResponse
	decodeBody(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("listed %d devices, want 2", len(listed))
	}

	var current int
	for _, dev := range listed {
		if dev.Current {
			current++
			if dev.ID != phone.ID {
				t.Errorf("the wrong device is marked current: %q", dev.ID)
			}
		}
	}
	if current != 1 {
		t.Errorf("%d devices marked current, want exactly 1", current)
	}
}

// The digest is not usable on its own, but handing it out would let anyone who
// read the response check a guessed token offline.
func TestListDevicesNeverLeaksTheHash(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("phone")

	resp := env.bearer(http.MethodGet, "/api/auth/devices", device.Token, nil)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	hash := auth.HashDeviceToken(device.Token)
	if bytes.Contains(body, []byte(hash)) {
		t.Error("the device list response contains the stored token digest")
	}
	if bytes.Contains(body, []byte(device.Token)) {
		t.Error("the device list response contains the token itself")
	}
}

func TestDeleteDeviceRevokesItsToken(t *testing.T) {
	env := newTestEnv(t)
	phone := env.pair("phone")
	tablet := env.pair("tablet")

	env.login()
	resp := env.do(http.MethodDelete, "/api/auth/devices/"+phone.ID, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deleting a device returned %d, want 204", resp.StatusCode)
	}

	dead := env.bearer(http.MethodGet, "/api/auth/me", phone.Token, nil)
	dead.Body.Close()
	if dead.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked token still works (%d)", dead.StatusCode)
	}

	// Revoking one device must not disturb the others.
	alive := env.bearer(http.MethodGet, "/api/auth/me", tablet.Token, nil)
	alive.Body.Close()
	if alive.StatusCode != http.StatusOK {
		t.Errorf("an unrelated device stopped working (%d)", alive.StatusCode)
	}
}

func TestDeleteUnknownDevice(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodDelete, "/api/auth/devices/does-not-exist", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleting an unknown device returned %d, want 404", resp.StatusCode)
	}
}

// Signing out is the only way an app can retire the one token it holds.
func TestLogoutUnpairsTheDevice(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("phone")

	resp := env.bearer(http.MethodPost, "/api/auth/logout", device.Token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout returned %d, want 204", resp.StatusCode)
	}

	after := env.bearer(http.MethodGet, "/api/auth/me", device.Token, nil)
	after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Errorf("the token still works after signing out (%d)", after.StatusCode)
	}
	if devices := env.storedDevices(t); len(devices) != 0 {
		t.Errorf("%d devices left on disk after signing out", len(devices))
	}
}

// A browser logging out must not unpair the operator's phone.
func TestSessionLogoutLeavesDevicesAlone(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("phone")

	env.login()
	resp := env.do(http.MethodPost, "/api/auth/logout", nil)
	resp.Body.Close()

	after := env.bearer(http.MethodGet, "/api/auth/me", device.Token, nil)
	after.Body.Close()
	if after.StatusCode != http.StatusOK {
		t.Errorf("a browser logout unpaired the device (%d)", after.StatusCode)
	}
}

func TestPasswordChangeUnpairsEveryDevice(t *testing.T) {
	env := newTestEnv(t)
	phone := env.pair("phone")
	tablet := env.pair("tablet")

	env.login()
	resp := env.do(http.MethodPost, "/api/auth/password", changePasswordRequest{
		CurrentPassword: testPass, NewPassword: "a-brand-new-password",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("changing the password returned %d, want 204", resp.StatusCode)
	}

	for name, token := range map[string]string{"phone": phone.Token, "tablet": tablet.Token} {
		after := env.bearer(http.MethodGet, "/api/auth/me", token, nil)
		after.Body.Close()
		if after.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s still works after a password change (%d)", name, after.StatusCode)
		}
	}
	if devices := env.storedDevices(t); len(devices) != 0 {
		t.Errorf("%d devices survived a password change on disk", len(devices))
	}
}

// A device token is worthless if it does not outlive the process: the whole
// point is that a self-update does not sign the operator's phone out.
func TestPairingSurvivesARestart(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("phone")

	stored := env.storedDevices(t)
	if len(stored) != 1 {
		t.Fatalf("panel.json holds %d devices, want 1", len(stored))
	}
	if stored[0].Hash != auth.HashDeviceToken(device.Token) {
		t.Error("the persisted hash does not match the issued token")
	}
	// Only the digest is written down; the token itself is unrecoverable.
	if stored[0].Hash == device.Token {
		t.Error("panel.json holds the token in the clear")
	}

	// A store rebuilt from the file is what the next boot gets.
	reloaded := auth.NewDeviceStore(stored)
	if _, ok := reloaded.Validate(device.Token); !ok {
		t.Error("the token does not work against a store reloaded from disk")
	}
}

// The console socket is the one endpoint a browser cannot put a header on, so
// it authenticates by cookie there. A native client has no such limit, and the
// panel must not force it onto some separate handshake to prove it.
func TestConsoleSocketAcceptsADeviceToken(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("phone")

	env.login()
	created := env.createInstance("bearer-console")

	header := http.Header{}
	header.Set("Authorization", "Bearer "+device.Token)
	wsURL := "ws" + strings.TrimPrefix(env.server.URL, "http")

	conn, resp, err := websocket.DefaultDialer.Dial(
		wsURL+"/api/instances/"+created.ID+"/console", header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dialling the console with a device token failed: %v (http %d)", err, status)
	}
	defer conn.Close()

	// The opening frame proves the socket is really live, not just upgraded.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var opening struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&opening); err != nil {
		t.Fatalf("read opening frame: %v", err)
	}
	if opening.Type != "history" {
		t.Errorf("opening frame is %q, want \"history\"", opening.Type)
	}
}

// A revoked device must lose the console too, not just the JSON API.
func TestConsoleSocketRejectsARevokedDevice(t *testing.T) {
	env := newTestEnv(t)
	device := env.pair("phone")

	env.login()
	created := env.createInstance("revoked-console")

	resp := env.do(http.MethodDelete, "/api/auth/devices/"+device.ID, nil)
	resp.Body.Close()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+device.Token)
	wsURL := "ws" + strings.TrimPrefix(env.server.URL, "http")

	conn, dialResp, err := websocket.DefaultDialer.Dial(
		wsURL+"/api/instances/"+created.ID+"/console", header)
	if err == nil {
		conn.Close()
		t.Fatal("a revoked device token still opened the console socket")
	}
	if dialResp == nil || dialResp.StatusCode != http.StatusUnauthorized {
		got := 0
		if dialResp != nil {
			got = dialResp.StatusCode
		}
		t.Errorf("dial was refused with %d, want 401", got)
	}
}

func (e *testEnv) storedDevices(t *testing.T) []auth.DeviceToken {
	t.Helper()
	panel, err := e.store.LoadPanel()
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	return panel.Devices
}
