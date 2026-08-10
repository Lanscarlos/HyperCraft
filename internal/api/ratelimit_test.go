package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// doWithHeaders is do() with extra headers, for exercising the forwarding
// logic that decides which client a request is counted against.
func (e *testEnv) doWithHeaders(method, path string, body any, headers map[string]string) *http.Response {
	e.t.Helper()

	req := e.request(method, path, body)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// failLogin makes one wrong-password attempt and returns its status.
func (e *testEnv) failLogin(headers map[string]string) *http.Response {
	e.t.Helper()
	return e.doWithHeaders(http.MethodPost, "/api/auth/login",
		loginRequest{Username: testUser, Password: "wrong"}, headers)
}

// ------------------------------------------------------- endpoint behaviour

// The panel's password check costs a tenth of a second of CPU, so an
// unthrottled login endpoint is both a way to guess the password and a way to
// take the machine away from the Minecraft servers on it.
func TestLoginThrottlesRepeatedFailures(t *testing.T) {
	env := newTestEnv(t)

	for i := range loginBurst {
		resp := env.failLogin(nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, resp.StatusCode)
		}
	}

	resp := env.failLogin(nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 once the burst is spent, got %d", resp.StatusCode)
	}

	retry, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After is not a number: %q", resp.Header.Get("Retry-After"))
	}
	if retry < 1 || retry > int(loginRefill.Seconds()) {
		t.Errorf("Retry-After of %ds is outside the refill window", retry)
	}
}

// A run of typos followed by the right password is an operator, not an attack,
// so signing in has to clear the address's history.
func TestSuccessfulLoginClearsTheThrottle(t *testing.T) {
	env := newTestEnv(t)

	for range loginBurst - 1 {
		env.failLogin(nil).Body.Close()
	}
	env.login()

	// Without the reset the budget would still be nearly spent and one of
	// these would come back 429.
	for i := range loginBurst - 1 {
		resp := env.failLogin(nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d after a good login: expected 401, got %d", i+1, resp.StatusCode)
		}
	}
}

// Pairing checks the same password and mints a longer-lived credential than a
// session, so it must not be a second budget to spend once login is exhausted.
func TestDevicePairingSharesTheLoginBudget(t *testing.T) {
	env := newTestEnv(t)

	for range loginBurst {
		env.failLogin(nil).Body.Close()
	}

	resp := env.do(http.MethodPost, "/api/auth/devices", createDeviceRequest{
		Username: testUser, Password: testPass, Name: "手机",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected pairing to be throttled on the login budget, got %d", resp.StatusCode)
	}
}

// X-Forwarded-For is a header any client can write. With no trusted proxies
// configured it must not be able to hand an attacker a fresh bucket per
// request, which would walk straight past the limiter.
func TestForwardedForCannotEvadeTheThrottleByDefault(t *testing.T) {
	env := newTestEnv(t)

	for i := range loginBurst {
		resp := env.failLogin(map[string]string{
			"X-Forwarded-For": fmt.Sprintf("203.0.113.%d", i+1),
		})
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, resp.StatusCode)
		}
	}

	resp := env.failLogin(map[string]string{"X-Forwarded-For": "203.0.113.200"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a spoofed X-Forwarded-For got a fresh budget: expected 429, got %d", resp.StatusCode)
	}
}

// Behind an accelerator every request arrives from a handful of back-to-origin
// addresses. Counting them all as one client would let a single attacker lock
// out everybody, so a listed proxy is believed and clients are told apart.
func TestTrustedProxySeparatesClients(t *testing.T) {
	env := newTestEnv(t, func(o *Options) {
		o.Panel.TrustedProxies = []string{"127.0.0.1", "::1"}
	})

	attacker := map[string]string{"X-Forwarded-For": "203.0.113.9"}
	for i := range loginBurst {
		resp := env.failLogin(attacker)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, resp.StatusCode)
		}
	}

	resp := env.failLogin(attacker)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected the noisy client to be throttled, got %d", resp.StatusCode)
	}

	// A different client behind the same proxy is unaffected.
	resp = env.failLogin(map[string]string{"X-Forwarded-For": "203.0.113.10"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a second client behind the proxy was locked out too: got %d", resp.StatusCode)
	}
}

// ------------------------------------------------------------- client address

func TestClientAddr(t *testing.T) {
	cases := []struct {
		name    string
		trusted []string
		remote  string
		xff     []string
		want    string
	}{
		{
			name:   "no trusted proxies, the header is ignored",
			remote: "203.0.113.5:1234",
			xff:    []string{"1.2.3.4"},
			want:   "203.0.113.5",
		},
		{
			name:    "an unlisted peer is not believed",
			trusted: []string{"10.0.0.0/8"},
			remote:  "203.0.113.5:1234",
			xff:     []string{"1.2.3.4"},
			want:    "203.0.113.5",
		},
		{
			name:    "a listed peer is believed",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.1.2.3:1234",
			xff:     []string{"1.2.3.4"},
			want:    "1.2.3.4",
		},
		{
			// The proxy appends what it saw; anything to the left of that was
			// written by the client and must not win.
			name:    "a client-written prefix is skipped",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.1.2.3:1234",
			xff:     []string{"9.9.9.9, 1.2.3.4"},
			want:    "1.2.3.4",
		},
		{
			name:    "trusted hops are walked past",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.1.2.3:1234",
			xff:     []string{"1.2.3.4, 10.9.9.9"},
			want:    "1.2.3.4",
		},
		{
			name:    "every hop trusted falls back to the peer",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.1.2.3:1234",
			xff:     []string{"10.5.5.5"},
			want:    "10.1.2.3",
		},
		{
			name:    "a listed peer with no header falls back to itself",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.1.2.3:1234",
			want:    "10.1.2.3",
		},
		{
			name:    "a chain split across two header lines",
			trusted: []string{"10.0.0.0/8"},
			remote:  "10.1.2.3:1234",
			xff:     []string{"1.2.3.4", "10.9.9.9"},
			want:    "1.2.3.4",
		},
		{
			name:   "ipv6 peer",
			remote: "[2001:db8::1]:443",
			want:   "2001:db8::1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trusted, bad := parseTrustedProxies(tc.trusted)
			if len(bad) > 0 {
				t.Fatalf("test fixture has unparseable proxies: %v", bad)
			}
			s := &Server{trustedProxies: trusted}

			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			req.RemoteAddr = tc.remote
			for _, value := range tc.xff {
				req.Header.Add("X-Forwarded-For", value)
			}

			if got := s.clientAddr(req); got != tc.want {
				t.Errorf("clientAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	prefixes, bad := parseTrustedProxies([]string{
		"10.0.0.0/8", "203.0.113.7", "  ", "not-an-ip", "2001:db8::/32", "",
	})

	if len(prefixes) != 3 {
		t.Errorf("expected 3 usable prefixes, got %d: %v", len(prefixes), prefixes)
	}
	if len(bad) != 1 || bad[0] != "not-an-ip" {
		t.Errorf("expected only %q to be rejected, got %v", "not-an-ip", bad)
	}
	// A bare address has to become a single host, not a wildcard.
	if got := prefixes[1].String(); got != "203.0.113.7/32" {
		t.Errorf("a bare address became %q, want 203.0.113.7/32", got)
	}
}

// ------------------------------------------------------------- token bucket

func TestRateLimiterRefillsOverTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newRateLimiter(2, time.Minute)
	limiter.now = func() time.Time { return now }

	limiter.penalise("a")
	limiter.penalise("a")
	retry, ok := limiter.allow("a")
	if ok {
		t.Fatal("the bucket should be empty after two failures")
	}
	if retry != time.Minute {
		t.Errorf("retry-after = %s, want a full refill of %s", retry, time.Minute)
	}

	now = now.Add(30 * time.Second)
	if _, ok := limiter.allow("a"); ok {
		t.Error("half a token should not be enough for an attempt")
	}

	now = now.Add(30 * time.Second)
	if _, ok := limiter.allow("a"); !ok {
		t.Error("a full token should allow an attempt again")
	}
}

// A flood must not dig the bucket into a deficit that keeps growing: the
// address locked out by that would as likely be the operator retrying.
func TestRateLimiterDoesNotGoIntoDeficit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newRateLimiter(1, time.Minute)
	limiter.now = func() time.Time { return now }

	for range 100 {
		limiter.penalise("a")
	}

	now = now.Add(time.Minute)
	if _, ok := limiter.allow("a"); !ok {
		t.Error("one refill period should clear the penalty however many attempts were made")
	}
}

func TestRateLimiterResetClearsHistory(t *testing.T) {
	limiter := newRateLimiter(1, time.Minute)
	limiter.penalise("a")
	if _, ok := limiter.allow("a"); ok {
		t.Fatal("the bucket should be empty")
	}

	limiter.reset("a")
	if _, ok := limiter.allow("a"); !ok {
		t.Error("reset should restore the full burst")
	}
}

func TestRateLimiterSweepsIdleBuckets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newRateLimiter(1, time.Minute)
	limiter.now = func() time.Time { return now }

	limiter.penalise("a")
	now = now.Add(rateBucketIdle + time.Second)
	limiter.penalise("b")
	limiter.sweep()

	if _, ok := limiter.buckets["a"]; ok {
		t.Error("an idle bucket should have been swept")
	}
	if _, ok := limiter.buckets["b"]; !ok {
		t.Error("a fresh bucket should have survived the sweep")
	}
}

// Spraying attempts from many addresses must not grow the table without bound.
func TestRateLimiterCapsItsTable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newRateLimiter(1, time.Minute)
	limiter.now = func() time.Time { return now }

	for i := range maxRateBuckets + 500 {
		limiter.penalise(fmt.Sprintf("10.%d.%d.%d", i/65536, (i/256)%256, i%256))
	}

	// The cap, plus the one shared bucket everything overflowing lands in.
	if len(limiter.buckets) > maxRateBuckets+1 {
		t.Errorf("table grew to %d buckets, cap is %d", len(limiter.buckets), maxRateBuckets)
	}
}

// -------------------------------------------------------------- derivation gate

func TestKDFGateRefusesWhenSaturated(t *testing.T) {
	gate := newKDFGate(1, 10*time.Millisecond)

	if !gate.enter(context.Background()) {
		t.Fatal("the first caller should get the slot")
	}
	if gate.enter(context.Background()) {
		t.Error("a second caller should be refused while the only slot is held")
	}

	gate.leave()
	if !gate.enter(context.Background()) {
		t.Error("the slot should be reusable once released")
	}
	gate.leave()
}

// A client that hangs up while queuing must not keep waiting on its behalf.
func TestKDFGateGivesUpWithTheClient(t *testing.T) {
	gate := newKDFGate(1, time.Minute)
	if !gate.enter(context.Background()) {
		t.Fatal("the first caller should get the slot")
	}
	defer gate.leave()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if gate.enter(ctx) {
		t.Error("a cancelled request should not be handed a slot")
	}
}

func TestDefaultKDFSlotsLeavesHeadroom(t *testing.T) {
	if got := defaultKDFSlots(); got < 2 {
		t.Errorf("defaultKDFSlots = %d, the floor is 2", got)
	}
}

// Changing the password checks the same credential login does, and behind a
// session is not the same as out of reach: a borrowed session would otherwise
// be an unmetered oracle for the panel password while the front door was
// throttled.
func TestPasswordChangeSharesTheLoginBudget(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	change := func(current string) *http.Response {
		return env.do(http.MethodPost, "/api/auth/password",
			changePasswordRequest{CurrentPassword: current, NewPassword: "a-new-password"})
	}

	for i := range loginBurst {
		resp := change("wrong")
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, resp.StatusCode)
		}
	}

	resp := change("wrong")
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429 once the budget is spent, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("a throttled response must say when to come back")
	}

	// And the budget really is shared: the front door is closed too.
	login := env.failLogin(nil)
	login.Body.Close()
	if login.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected login to be throttled as well, got %d", login.StatusCode)
	}
}

// The right current password is not an attack, so it must clear the history the
// typos before it left behind.
func TestSuccessfulPasswordChangeClearsTheThrottle(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	for range loginBurst - 1 {
		resp := env.do(http.MethodPost, "/api/auth/password",
			changePasswordRequest{CurrentPassword: "wrong", NewPassword: "a-new-password"})
		resp.Body.Close()
	}

	resp := env.do(http.MethodPost, "/api/auth/password",
		changePasswordRequest{CurrentPassword: testPass, NewPassword: "a-new-password"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected the change to go through, got %d", resp.StatusCode)
	}

	// The change signed every session out, so login is the only thing left to
	// prove the address is no longer being counted against.
	login := env.failLogin(nil)
	login.Body.Close()
	if login.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected the throttle to have been reset, got %d", login.StatusCode)
	}
}

// ------------------------------------------------------------- stream gate

func TestStreamGateCapsAndReleases(t *testing.T) {
	gate := newStreamGate(2)

	first, ok := gate.enter("a")
	if !ok {
		t.Fatal("the first slot was refused")
	}
	second, ok := gate.enter("a")
	if !ok {
		t.Fatal("the second slot was refused")
	}
	if _, ok := gate.enter("a"); ok {
		t.Error("the third slot should have been refused")
	}
	// A different key has its own budget.
	if _, ok := gate.enter("b"); !ok {
		t.Error("an unrelated key was refused")
	}

	first()
	if _, ok := gate.enter("a"); !ok {
		t.Error("releasing a slot did not free it")
	}
	second()

	// Releasing twice must not hand out a slot that is still held, which is
	// what a handler that both defers the release and calls it early would do.
	second()
	second()
	if gate.active() != 2 {
		t.Errorf("active = %d, want 2 after the double releases", gate.active())
	}
}

func TestStreamGateForgetsIdleKeys(t *testing.T) {
	gate := newStreamGate(1)

	release, ok := gate.enter("a")
	if !ok {
		t.Fatal("the first slot was refused")
	}
	release()

	gate.mu.Lock()
	held := len(gate.held)
	gate.mu.Unlock()
	if held != 0 {
		t.Errorf("the table kept %d entries after everything closed", held)
	}
}
