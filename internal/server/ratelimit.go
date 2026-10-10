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

// Do not trust X-Forwarded-For: without a trusted-proxy configuration the
// caller could forge it to bypass a per-source limit.
func keyOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

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

// A successful authentication clears prior typos rather than accumulating
// unrelated mistakes into a later lockout.
func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
