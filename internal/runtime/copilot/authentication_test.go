package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
)

func TestAuthenticationStatusRequiresAccountAndHost(t *testing.T) {
	login, host := "Developer-B", "github.com"
	privateDetail := "private-provider-detail"
	for _, name := range []string{"match", "case", "missing", "unauthenticated", "login", "host", "other-login", "other-host"} {
		t.Run(name, func(t *testing.T) {
			status := &sdk.GetAuthStatusResponse{
				IsAuthenticated: true, Login: &login, Host: &host, StatusMessage: &privateDetail, AuthType: &privateDetail,
			}
			switch name {
			case "case":
				upperHost := "GITHUB.COM"
				status.Host = &upperHost
			case "missing":
				status = nil
			case "unauthenticated":
				status.IsAuthenticated = false
			case "login":
				status.Login = nil
			case "host":
				status.Host = nil
			case "other-login":
				status.Login = &privateDetail
			case "other-host":
				status.Host = &privateDetail
			}
			err := checkAuthenticationStatus(status, "developer-b")
			if (err == nil) != (name == "match" || name == "case") {
				t.Fatal("authentication boundary mismatch", err)
			}
			if err != nil && strings.Contains(err.Error(), privateDetail) {
				t.Fatal("provider detail leaked")
			}
		})
	}
}

func TestAuthenticationProbeRejectsInvalidCancelledAndClosedRequests(t *testing.T) {
	adapter, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	for _, account := range []string{"", " ", " developer-b", "developer-b "} {
		if err := adapter.CheckAuthentication(context.Background(), account); err == nil {
			t.Fatal("invalid account accepted")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.CheckAuthentication(ctx, "developer-b"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if adapter.started || len(adapter.sessions) != 0 {
		t.Fatal("rejected probe started a client or created a session")
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := adapter.CheckAuthentication(context.Background(), "developer-b"); !errors.Is(err, ErrClosedAdapter) {
		t.Fatal("closed adapter accepted", err)
	}
}

func TestAuthenticationProbeUsesSDKRPCWithoutCreatingSessions(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	results := []string{
		`{"protocolVersion":3}`,
		`{"isAuthenticated":true,"login":"developer-b","host":"github.com"}`,
		`{"isAuthenticated":true,"login":"private-provider-detail","host":"github.com"}`,
		`null`,
		`{"isAuthenticated":"private-provider-detail"}`,
		`{"code":-32603,"message":"private-provider-detail"}`,
		`{"isAuthenticated":true,"login":"developer-b","host":"github.com"}`,
	}
	done := make(chan error, 1)
	go func() {
		done <- func() error {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return err
			}
			reader := bufio.NewReader(conn)
			for i, result := range results {
				header, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				var size int
				if _, err := fmt.Sscanf(header, "Content-Length: %d", &size); err != nil {
					return err
				}
				if line, err := reader.ReadString('\n'); err != nil || line != "\r\n" {
					return fmt.Errorf("invalid protocol header: %w", err)
				}
				body := make([]byte, size)
				if _, err := io.ReadFull(reader, body); err != nil {
					return err
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					return err
				}
				want := "auth.getStatus"
				if i == 0 {
					want = "connect"
				}
				if request.Method != want {
					return fmt.Errorf("unexpected RPC method %q", request.Method)
				}
				field := "result"
				if i == 5 {
					field = "error"
				}
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"%s":%s}`, request.ID, field, result)
				if _, err := fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(reply), reply); err != nil {
					return err
				}
			}
			return nil
		}()
	}()
	adapter, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter.client = sdk.NewClient(&sdk.ClientOptions{
		Connection: sdk.URIConnection{URL: listener.Addr().String()},
	})
	defer adapter.Close()
	hash := adapter.ConfigHash()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 1; i < len(results); i++ {
		err := adapter.CheckAuthentication(ctx, "developer-b")
		if (err == nil) != (i == 1 || i == 6) {
			t.Fatalf("RPC authentication result %d: %v", i, err)
		}
		if err != nil && strings.Contains(err.Error(), "private-provider-detail") {
			t.Fatal("RPC error or provider response leaked")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(adapter.sessions) != 0 || adapter.ConfigHash() != hash {
		t.Fatal("authentication probe created a session or changed its binding")
	}
	if err := adapter.Close(); err != nil {
		t.Fatal("authentication client cleanup failed", err)
	}
}

func TestAuthenticationProbeRedactsStartupFailure(t *testing.T) {
	adapter, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = adapter.CheckAuthentication(ctx, "developer-b")
	if err == nil || !strings.Contains(err.Error(), "authentication client could not start") {
		t.Fatal("startup failure did not return a secret-safe authentication error", err)
	}
	if len(adapter.sessions) != 0 {
		t.Fatal("failed authentication probe created a session")
	}
}
