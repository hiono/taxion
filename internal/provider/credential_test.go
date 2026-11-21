package provider

import (
	"errors"
	"testing"
)

func TestCredentialLeaseScopesAndZeroesSecretCopies(t *testing.T) {
	secret := []byte("test-only-provider-secret")
	lease, err := NewCredentialLease(secret)
	if err != nil {
		t.Fatalf("NewCredentialLease: %v", err)
	}
	secret[0] = 'X'

	var exposed []byte
	if err := lease.WithSecret(func(value []byte) error {
		exposed = value
		if string(value) != "test-only-provider-secret" {
			t.Fatalf("credential copy = %q", value)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithSecret: %v", err)
	}
	for i, value := range exposed {
		if value != 0 {
			t.Fatalf("credential copy byte %d was not zeroed", i)
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := lease.WithSecret(func([]byte) error { return nil }); !errors.Is(err, ErrCredentialLeaseClosed) {
		t.Fatalf("WithSecret after Close = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("idempotent Close: %v", err)
	}
}

func TestCredentialLeaseRejectsEmptySecretAndNilCallback(t *testing.T) {
	if _, err := NewCredentialLease(nil); !errors.Is(err, ErrEmptyCredential) {
		t.Fatalf("empty secret error = %v", err)
	}
	lease, err := NewCredentialLease([]byte("secret"))
	if err != nil {
		t.Fatalf("NewCredentialLease: %v", err)
	}
	defer lease.Close()
	if err := lease.WithSecret(nil); err == nil {
		t.Fatal("nil callback was accepted")
	}
}

func TestCredentialLeaseCanCloseFromCallback(t *testing.T) {
	lease, err := NewCredentialLease([]byte("test-only-provider-secret"))
	if err != nil {
		t.Fatalf("NewCredentialLease: %v", err)
	}
	if err := lease.WithSecret(func(value []byte) error {
		if string(value) != "test-only-provider-secret" {
			t.Fatalf("credential copy = %q", value)
		}
		return lease.Close()
	}); err != nil {
		t.Fatalf("WithSecret callback Close: %v", err)
	}
	if err := lease.WithSecret(func([]byte) error { return nil }); !errors.Is(err, ErrCredentialLeaseClosed) {
		t.Fatalf("WithSecret after callback Close = %v", err)
	}
}
