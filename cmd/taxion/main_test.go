package main

import (
	"strings"
	"testing"
)

func TestUsageDescribesCurrentCommands(t *testing.T) {
	for _, text := range []string{
		"memory search", "--query required", "memory export", "no transmission",
		"Bind coder-output evidence", "local stub",
		"task create, dispatch and review require an explicit --repo",
	} {
		if !strings.Contains(usage, text) {
			t.Errorf("usage missing %q", text)
		}
	}
}

func TestCheckServeMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		addr    string
		token   string
		wantErr bool
	}{
		{"loopback default addr", "loopback", "127.0.0.1:3938", "", false},
		{"loopback tokened local", "loopback", "127.0.0.1:3938", "tok", false},
		{"loopback refuses wildcard untokened", "loopback", "0.0.0.0:3938", "", true},
		{"loopback refuses wildcard even tokened", "loopback", "0.0.0.0:3938", "tok", true},
		{"remote requires token", "remote", "0.0.0.0:3938", "", true},
		{"remote tokened wildcard", "remote", "0.0.0.0:3938", "tok", false},
		{"unknown mode", "dmz", "127.0.0.1:3938", "tok", true},
	}
	for _, tc := range cases {
		err := checkServeMode(tc.mode, tc.addr, tc.token)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", tc.name, err, tc.wantErr)
		}
	}
}
