package openacp

import (
	"strings"
	"sync"
)

// Keep only a bounded in-memory tail; neither stderr nor RPC details are
// published. A recognized phrase is a reported signal, not a proven cause.
type diagnosticStderr struct {
	mu   sync.Mutex
	tail []byte
}

func (s *diagnosticStderr) Write(p []byte) (int, error) {
	const limit = 4096
	n := len(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(p) >= limit {
		s.tail = append(s.tail[:0], p[len(p)-limit:]...)
	} else {
		s.tail = append(s.tail, p...)
		if len(s.tail) > limit {
			copy(s.tail, s.tail[len(s.tail)-limit:])
			s.tail = s.tail[:limit]
		}
	}
	return n, nil
}

func (s *diagnosticStderr) signal() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return providerSignal(string(s.tail))
}

func (s *diagnosticStderr) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tail = nil
}

func providerSignal(details string) string {
	if strings.Contains(strings.ToLower(details), "free usage limit reached") {
		return "free-usage-limit-reported"
	}
	return "unknown"
}
