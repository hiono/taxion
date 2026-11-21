package provider

import (
	"errors"
	"sync"
)

var (
	ErrEmptyCredential       = errors.New("Provider credential is empty")
	ErrCredentialLeaseClosed = errors.New("Provider credential lease is closed")
)

type CredentialLease interface {
	// WithSecret exposes a short-lived copy only to the trusted Adapter callback.
	// The copy is zeroed after the callback returns and must not be retained.
	WithSecret(func([]byte) error) error
	Close() error
}

type CredentialBroker interface {
	Acquire(context.Context, Identity) (CredentialLease, error)
}

type memoryCredentialLease struct {
	mu     sync.RWMutex
	secret []byte
	closed bool
}

func NewCredentialLease(secret []byte) (CredentialLease, error) {
	if len(secret) == 0 {
		return nil, ErrEmptyCredential
	}
	return &memoryCredentialLease{secret: append([]byte(nil), secret...)}, nil
}

func (l *memoryCredentialLease) WithSecret(use func([]byte) error) error {
	if use == nil {
		return errors.New("credential callback is required")
	}
	l.mu.RLock()
	if l.closed {
		l.mu.RUnlock()
		return ErrCredentialLeaseClosed
	}
	copyOfSecret := append([]byte(nil), l.secret...)
	defer l.mu.RUnlock()
	defer zero(copyOfSecret)
	return use(copyOfSecret)
}

func (l *memoryCredentialLease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	zero(l.secret)
	l.secret = nil
	l.closed = true
	return nil
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
