package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The panel's password check is deliberately expensive — 210k rounds of
// PBKDF2, around a tenth of a second of one core (see internal/auth). That is
// the right cost for a stored credential and the wrong thing to hand an
// anonymous caller unbounded: /api/auth/login and /api/auth/devices both run a
// derivation before they know who is asking, so a few dozen concurrent
// requests would saturate the CPU of the machine the Minecraft servers are
// running on. Guessing the password is not even required for that.
//
// Two separate limits answer two separate problems:
//
//   - rateLimiter counts failed attempts per client address, which is what
//     makes online password guessing impractical.
//   - kdfGate caps how many derivations run at once, which is what protects
//     the CPU no matter how many addresses the attempts are spread across.
//
// Neither subsumes the other. A single address hammering one endpoint is
// stopped by the first; a botnet, or everything arriving from one accelerator's
// back-to-origin addresses, is only bounded by the second.
const (
	// loginBurst is how many failed checks an address may make back to back
	// before it has to wait. Enough for a genuine run of typos.
	loginBurst = 5
	// loginRefill is how long one spent attempt takes to come back, so a
	// throttled address still gets two tries a minute rather than being locked
	// out until someone intervenes.
	loginRefill = 30 * time.Second
	// rateBucketIdle is how long a bucket outlives its last request. Anything
	// older has refilled completely and says nothing, so it is dropped.
	rateBucketIdle = 15 * time.Minute
	// maxRateBuckets caps the table so that spraying attempts from many
	// addresses cannot grow it without bound. Once it is full, new addresses
	// share a single bucket — under exactly the attack that fills the table,
	// degrading to one global limit is the safe way to run out of room.
	maxRateBuckets = 8192
	// overflowKey is that shared bucket. No address parses to the empty
	// string, so it cannot collide with a real one.
	overflowKey = ""
	// kdfWait is how long a request will queue for a derivation slot before
	// giving up. Long enough to absorb a burst of honest logins, short enough
	// that a flood is refused instead of piling up.
	kdfWait = 2 * time.Second
)

// rateLimiter is a token bucket per key, refilling continuously.
//
// Only failures spend a token: an operator who signs in correctly a hundred
// times in a row is not doing the thing this exists to stop, and making the
// panel throttle its own UI would be a bug rather than a defence.
type rateLimiter struct {
	burst  float64
	refill time.Duration
	// now is swappable so tests can advance time instead of sleeping through
	// a refill that is measured in half-minutes.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*rateBucket
}

type rateBucket struct {
	tokens float64
	seen   time.Time
}

func newRateLimiter(burst int, refill time.Duration) *rateLimiter {
	return &rateLimiter{
		burst:   float64(burst),
		refill:  refill,
		now:     time.Now,
		buckets: make(map[string]*rateBucket),
	}
}

// allow reports whether key has an attempt left, and if not, how long until it
// does. It does not spend anything: the caller cannot know yet whether this
// attempt is a failure, and only failures are charged.
func (l *rateLimiter) allow(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.bucketLocked(key, l.now())
	if bucket.tokens >= 1 {
		return 0, true
	}
	return time.Duration((1 - bucket.tokens) * float64(l.refill)), false
}

// penalise charges key for a failed attempt.
func (l *rateLimiter) penalise(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.bucketLocked(key, l.now())
	bucket.tokens--
	// Never below empty: letting a flood dig the bucket into deficit would
	// turn a rate limit into an ever-growing lockout, and the address that
	// gets locked out that way is as likely to be the operator retrying as
	// the attacker.
	if bucket.tokens < 0 {
		bucket.tokens = 0
	}
}

// reset clears key's history, called when a check succeeds.
func (l *rateLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// sweep drops buckets nobody has touched recently; call it periodically.
func (l *rateLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(l.now())
}

func (l *rateLimiter) sweepLocked(now time.Time) {
	cutoff := now.Add(-rateBucketIdle)
	for key, bucket := range l.buckets {
		if bucket.seen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// bucketLocked returns key's bucket, refilled to the current time and creating
// it if this is the first the limiter has heard of the address.
func (l *rateLimiter) bucketLocked(key string, now time.Time) *rateBucket {
	bucket, ok := l.buckets[key]
	if ok {
		if elapsed := now.Sub(bucket.seen); elapsed > 0 {
			bucket.tokens = min(l.burst, bucket.tokens+elapsed.Seconds()/l.refill.Seconds())
		}
		bucket.seen = now
		return bucket
	}

	if len(l.buckets) >= maxRateBuckets {
		l.sweepLocked(now)
		if len(l.buckets) >= maxRateBuckets {
			key = overflowKey
			if shared, ok := l.buckets[key]; ok {
				shared.seen = now
				return shared
			}
		}
	}

	bucket = &rateBucket{tokens: l.burst, seen: now}
	l.buckets[key] = bucket
	return bucket
}

// kdfGate bounds how many password derivations are in flight at once.
//
// The cap is a fraction of the machine rather than all of it: the panel shares
// these cores with the Minecraft servers it exists to run, and a login storm
// making the servers stutter would be its own kind of outage.
type kdfGate struct {
	slots chan struct{}
	wait  time.Duration
}

func newKDFGate(slots int, wait time.Duration) *kdfGate {
	if slots < 1 {
		slots = 1
	}
	return &kdfGate{slots: make(chan struct{}, slots), wait: wait}
}

// defaultKDFSlots leaves most of the machine to the servers. Two is the floor
// because one would serialise the operator behind a single attacker's request.
func defaultKDFSlots() int {
	return max(2, runtime.GOMAXPROCS(0)/4)
}

// enter takes a slot, reporting false if none came free in time or the client
// hung up while waiting.
func (g *kdfGate) enter(ctx context.Context) bool {
	timer := time.NewTimer(g.wait)
	defer timer.Stop()

	select {
	case g.slots <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (g *kdfGate) leave() { <-g.slots }

// ------------------------------------------------------------ client address

// clientAddr is the address a request is counted against.
//
// It is r.RemoteAddr unless that peer is a configured trusted proxy, in which
// case the client it is speaking for is read out of X-Forwarded-For. The order
// matters: the header is plain text any client can write, so believing it from
// an unlisted peer would let an attacker pick their own rate-limit bucket —
// a fresh one per request — and walk straight past the limiter.
func (s *Server) clientAddr(r *http.Request) string {
	peer, ok := peerAddr(r)
	if !ok {
		// Unparseable, which should not happen over TCP. Counting every such
		// request against one bucket is the safe direction to be wrong in.
		return "unknown"
	}
	if !s.isTrustedProxy(peer) {
		return peer.String()
	}

	// Each hop appends what it saw to the right, so the rightmost entry that
	// is not itself a trusted proxy is the furthest back we have any reason to
	// believe. Reading from the left would take whatever the client wrote.
	for _, header := range slicesReverse(r.Header.Values("X-Forwarded-For")) {
		fields := strings.Split(header, ",")
		for i := len(fields) - 1; i >= 0; i-- {
			addr, err := netip.ParseAddr(strings.TrimSpace(fields[i]))
			if err != nil {
				continue
			}
			if addr = addr.Unmap(); !s.isTrustedProxy(addr) {
				return addr.String()
			}
		}
	}
	// Every hop was a trusted proxy, or the header was absent. The peer is all
	// there is.
	return peer.String()
}

func (s *Server) isTrustedProxy(addr netip.Addr) bool {
	for _, prefix := range s.trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// peerAddr is the address the TCP connection actually came from.
func peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// parseTrustedProxies turns the configured entries into prefixes, accepting a
// bare address as a single host. Unparseable entries are returned rather than
// rejected: a typo in one line should be reported and skipped, not stop a
// panel — with its Minecraft servers — from starting.
func parseTrustedProxies(entries []string) ([]netip.Prefix, []string) {
	var (
		prefixes []netip.Prefix
		bad      []string
	)
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			addr = addr.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		bad = append(bad, entry)
	}
	return prefixes, bad
}

// slicesReverse iterates a header's values newest-first. X-Forwarded-For is
// almost always a single header, but a proxy chain may add its own line rather
// than extending the existing one.
func slicesReverse(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[len(values)-1-i] = v
	}
	return out
}
