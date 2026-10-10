// Package server — rate limiting (M8-E).
//
// Brute-force protection for the Bearer gate: per-source-IP sliding-window
// failure counting with lockout. No X-Forwarded-For is trusted (spoofable)
// — the key is always the connection's RemoteAddr host.
//
// Policy (fixed, no flags): 5 failed logins within 1 minute locks the IP out
// for 5 minutes; locked-out requests answer 429. One success resets the IP's
// counter. When no token is configured there is nothing to guess, so the
// limiter stays out of the way.
package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	rateMaxFails = 5
	rateWindow   = time.Minute
	rateLockout  = 5 * time.Minute
)

// limiter counts auth failures per IP and locks out brute forcers.
type limiter struct {
	maxFails int
	window   time.Duration
	lockout  time.Duration

	mu     sync.Mutex
	fails  map[string][]time.Time
	locked map[string]time.Time
}

func newLimiter(maxFails int, window, lockout time.Duration) *limiter {
	return &limiter{
		maxFails: maxFails,
		window:   window,
		lockout:  lockout,
		fails:    map[string][]time.Time{},
		locked:   map[string]time.Time{},
	}
}

// keyOf returns the connection's host — never a client-supplied header.
func keyOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allow reports whether ip may attempt auth now.
func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	until, ok := l.locked[ip]
	if !ok {
		return true
	}
	if now.Before(until) {
		return false
	}
	delete(l.locked, ip)
	return true
}

// fail records a failed login; crossing maxFails within window locks the IP.
func (l *limiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	l.fails[ip] = kept
	if len(kept) >= l.maxFails {
		l.locked[ip] = now.Add(l.lockout)
		delete(l.fails, ip)
	}
}

// reset clears an IP's counter after one success — typos must not accumulate.
func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
