package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// ------------------------------------------------------------ endpoint

func TestAuthEventsRecordSignInAndFailure(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.failLogin(nil).Body.Close()

	resp := env.do(http.MethodGet, "/api/auth/events", nil)
	var events []authEvent
	decodeBody(t, resp, &events)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if len(events) != 2 {
		t.Fatalf("expected the sign-in and the failure, got %d events: %+v", len(events), events)
	}
	// Newest first: the failure came second.
	if events[0].Kind != eventSignInFailed {
		t.Errorf("newest event = %q, want %q", events[0].Kind, eventSignInFailed)
	}
	if events[1].Kind != eventSignIn {
		t.Errorf("older event = %q, want %q", events[1].Kind, eventSignIn)
	}
	if events[0].Client == "" || events[0].Remote == "" {
		t.Errorf("both addresses should be filled in: %+v", events[0])
	}
	// The peer must not carry the ephemeral port, or repeats never collapse.
	if host := events[0].Remote; host != "127.0.0.1" {
		t.Errorf("remote = %q, want the bare host 127.0.0.1", host)
	}
}

// The addresses in this list are exactly what someone probing the panel would
// like to learn about its traffic.
func TestAuthEventsRequireASession(t *testing.T) {
	env := newTestEnv(t)

	resp := env.do(http.MethodGet, "/api/auth/events", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without a session, got %d", resp.StatusCode)
	}
}

// A flood of refused attempts must cost one row, not the whole ring.
func TestAuthEventsFoldThrottling(t *testing.T) {
	env := newTestEnv(t)
	// Sign in first: once the budget is spent there is no way back in, and a
	// successful login resets the bucket so the failures below start from full.
	env.login()

	for range loginBurst + 5 {
		env.failLogin(nil).Body.Close()
	}

	// The session cookie survives the failed attempts — a wrong password does
	// not disturb a session that is already established.
	resp := env.do(http.MethodGet, "/api/auth/events", nil)
	var events []authEvent
	decodeBody(t, resp, &events)

	var throttled, failures int
	for _, event := range events {
		switch event.Kind {
		case eventThrottled:
			throttled++
			if event.Count < 5 {
				t.Errorf("throttle row should carry its repeat count, got %d", event.Count)
			}
		case eventSignInFailed:
			failures++
		}
	}
	if throttled != 1 {
		t.Errorf("expected the throttled events to fold into one row, got %d", throttled)
	}
	if failures != 1 {
		t.Errorf("expected the failures to fold into one row, got %d", failures)
	}
	// Newest first, and the whole burst has cost three rows: the sign-in, the
	// folded failures, the folded throttling.
	if len(events) != 3 {
		t.Fatalf("expected 3 rows, got %d: %+v", len(events), events)
	}
	if events[0].Kind != eventThrottled || events[2].Kind != eventSignIn {
		t.Errorf("unexpected order: %q … %q", events[0].Kind, events[2].Kind)
	}
}

func TestMeReportsTheRequestAddresses(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		env := newTestEnv(t)
		env.login()

		resp := env.doWithHeaders(http.MethodGet, "/api/auth/me", nil, map[string]string{
			// Not believed: no trusted proxies are configured.
			"X-Forwarded-For": "203.0.113.9",
		})
		var user userResponse
		decodeBody(t, resp, &user)

		if user.Client != "127.0.0.1" || user.Remote != "127.0.0.1" {
			t.Errorf("client=%q remote=%q, want both 127.0.0.1", user.Client, user.Remote)
		}
	})

	t.Run("behind a trusted proxy", func(t *testing.T) {
		env := newTestEnv(t, func(o *Options) {
			o.Panel.TrustedProxies = []string{"127.0.0.1", "::1"}
		})
		env.login()

		resp := env.doWithHeaders(http.MethodGet, "/api/auth/me", nil, map[string]string{
			"X-Forwarded-For": "203.0.113.9",
		})
		var user userResponse
		decodeBody(t, resp, &user)

		if user.Client != "203.0.113.9" {
			t.Errorf("client = %q, want the forwarded address", user.Client)
		}
		if user.Remote != "127.0.0.1" {
			t.Errorf("remote = %q, want the proxy's own address", user.Remote)
		}
	})
}

// -------------------------------------------------------------- ring buffer

func TestAuthLogCollapsesConsecutiveRepeats(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	log := newAuthLog()
	log.now = func() time.Time { return now }

	repeat := authEvent{
		Kind: eventSignInFailed, Username: "admin",
		Client: "203.0.113.9", Remote: "10.1.2.3",
	}
	log.record(repeat)
	now = now.Add(time.Second)
	log.record(repeat)
	now = now.Add(time.Second)
	log.record(repeat)

	list := log.list()
	if len(list) != 1 {
		t.Fatalf("expected one folded row, got %d", len(list))
	}
	if list[0].Count != 3 {
		t.Errorf("count = %d, want 3", list[0].Count)
	}
	if !list[0].At.Equal(now) {
		t.Errorf("At = %s, want the most recent occurrence %s", list[0].At, now)
	}
}

// Folding must not hide a second client doing the same thing.
func TestAuthLogKeepsDifferentClientsApart(t *testing.T) {
	log := newAuthLog()

	log.record(authEvent{Kind: eventSignInFailed, Client: "203.0.113.9"})
	log.record(authEvent{Kind: eventSignInFailed, Client: "203.0.113.10"})
	log.record(authEvent{Kind: eventSignInFailed, Client: "203.0.113.9"})

	if list := log.list(); len(list) != 3 {
		t.Errorf("expected three rows for two alternating clients, got %d", len(list))
	}
}

func TestAuthLogKeepsTheNewestWhenFull(t *testing.T) {
	log := newAuthLog()
	for i := range authLogSize + 50 {
		log.record(authEvent{Kind: eventSignIn, Username: fmt.Sprintf("u%d", i)})
	}

	list := log.list()
	if len(list) != authLogSize {
		t.Fatalf("expected the ring to cap at %d, got %d", authLogSize, len(list))
	}
	if want := fmt.Sprintf("u%d", authLogSize+50-1); list[0].Username != want {
		t.Errorf("newest = %q, want %q", list[0].Username, want)
	}
	if want := fmt.Sprintf("u%d", 50); list[len(list)-1].Username != want {
		t.Errorf("oldest kept = %q, want %q", list[len(list)-1].Username, want)
	}
}

func TestAuthLogIsEmptyToStart(t *testing.T) {
	if list := newAuthLog().list(); len(list) != 0 {
		t.Errorf("a fresh log should be empty, got %d rows", len(list))
	}
}
