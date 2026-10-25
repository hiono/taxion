package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func quotaTestSelection(t *testing.T, account string) json.RawMessage {
	t.Helper()
	selected, err := config.Load([]byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: gh:active}
runtime: {provider: copilot, account: fixture, credentialRef: copilot:logged-in, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`))
	if err != nil {
		t.Fatal(err)
	}
	selected.Runtime.Account = account
	data, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAccountQuotaUsesOnlyAuthAndReadRPC(t *testing.T) {
	for _, test := range []struct {
		name, timestamp, state string
	}{
		{"fresh", time.Now().UTC().Format(time.RFC3339Nano), "available"},
		{"stale", time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano), "stale"},
		{"future", time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339Nano), "stale"},
		{"missing", "", "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testAccountQuotaReadRPC(t, test.timestamp, test.state)
		})
	}
}

func testAccountQuotaReadRPC(t *testing.T, timestamp, expectedState string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
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
			reader := textproto.NewReader(bufio.NewReader(conn))
			for _, step := range []struct{ method, result string }{
				{"connect", `{"protocolVersion":3}`},
				{"auth.getStatus", `{"isAuthenticated":true,"login":"fixture","host":"github.com"}`},
				{"account.getAllUsers", `[{"authInfo":{"type":"user","login":"fixture"},"selectionId":"selected","token":"synthetic-quota-token"}]`},
				{"account.getQuota", `{"quotaSnapshots":{"premium_interactions":{"entitlementRequests":50000,"usedRequests":12300,"remainingPercentage":75.4,"resetDate":"OBSERVATION"}}}`},
				{"account.getCurrentAuth", `{"authInfo":{"type":"user","host":"https://github.com","login":"fixture","copilotUser":{"quota_reset_date_utc":"2099-11-01T00:00:00.000Z"}}}`},
			} {
				header, err := reader.ReadMIMEHeader()
				if err != nil {
					return err
				}
				var size int
				if _, err := fmt.Sscanf(header.Get("Content-Length"), "%d", &size); err != nil || size <= 0 || size > 65536 {
					return fmt.Errorf("invalid fixture frame")
				}
				body := make([]byte, size)
				if _, err := io.ReadFull(reader.R, body); err != nil {
					return err
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.Unmarshal(body, &request); err != nil || request.Method != step.method {
					return fmt.Errorf("unexpected RPC method %q, want %q", request.Method, step.method)
				}
				if step.method == "account.getQuota" && !strings.Contains(string(request.Params), `"gitHubToken":"synthetic-quota-token"`) {
					return fmt.Errorf("quota request did not refresh the explicitly verified account")
				}
				value := "null"
				if timestamp != "" {
					value = fmt.Sprintf("%q", timestamp)
				}
				step.result = strings.ReplaceAll(step.result, `"OBSERVATION"`, value)
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, step.result)
				if _, err := fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(reply), reply); err != nil {
					return err
				}
			}
			if _, err := reader.R.Peek(1); !errors.Is(err, io.EOF) {
				return fmt.Errorf("expected client disconnect after account quota response: %v", err)
			}
			return nil
		}()
	}()
	cfg := testConfig(t)
	cfg.Configuration = quotaTestSelection(t, "fixture")
	cfg.BudgetGuidance, err = core.NewBudgetGuidance("", "account")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	adapter.client = sdk.NewClient(&sdk.ClientOptions{Connection: sdk.URIConnection{URL: listener.Addr().String()}})
	hash := adapter.ConfigHash()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := adapter.ReadAccountQuota(ctx, "fixture")
	if err != nil || result.State != expectedState || *result.Entitlement != 50000 || *result.Used != 12300 ||
		result.Remaining == nil || *result.Remaining != 37700 ||
		*result.RemainingPercentage != 75.4 || result.ResetAt == nil || !result.ResetAt.Equal(time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)) || result.ExpiresAt != nil ||
		result.Unit != "requests" || len(adapter.sessions) != 0 || adapter.ConfigHash() != hash {
		t.Fatal("read quota changed identity, created a session, or returned the wrong units", result, err)
	}
	if timestamp != "" && result.ObservedAt.Format(time.RFC3339Nano) != timestamp {
		t.Fatal("cached provider observation was relabelled with local time")
	}
	if err := adapter.Close(); err != nil {
		t.Fatal("quota client cleanup failed", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSelectedQuotaRefreshRejectsMissingAndAmbiguousCredentials(t *testing.T) {
	token := "synthetic-only-token"
	user := func(identity string, credential *string) rpc.AccountAllUsers {
		return rpc.AccountAllUsers{AuthInfo: rpc.RawAuthInfoData{Raw: json.RawMessage(identity)}, Token: credential}
	}
	for _, test := range []struct {
		name  string
		users rpc.AccountGetAllUsersResult
		ok    bool
	}{
		{"selected", rpc.AccountGetAllUsersResult{user(`{"login":"fixture"}`, &token)}, true},
		{"nested", rpc.AccountGetAllUsersResult{user(`{"user":{"login":"fixture"}}`, &token)}, true},
		{"other", rpc.AccountGetAllUsersResult{user(`{"login":"other"}`, &token)}, false},
		{"missing-token", rpc.AccountGetAllUsersResult{user(`{"login":"fixture"}`, nil)}, false},
		{"duplicate", rpc.AccountGetAllUsersResult{user(`{"login":"fixture"}`, &token), user(`{"login":"fixture"}`, &token)}, false},
		{"conflicting", rpc.AccountGetAllUsersResult{user(`{"login":"fixture","user":{"login":"other"}}`, &token)}, false},
		{"malformed", rpc.AccountGetAllUsersResult{user(`null`, &token)}, false},
		{"empty", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := selectedQuotaRequest(test.users, "fixture")
			if test.ok && (err != nil || request == nil || request.GitHubToken == nil || *request.GitHubToken != token || request.SelectionID != nil) ||
				!test.ok && (err == nil || request != nil) {
				t.Fatal("incorrect selected-account refresh result")
			}
			if err != nil && strings.Contains(err.Error(), token) {
				t.Fatal("credential included in error")
			}
		})
	}
}
func TestAccountQuotaMappingAndUnapprovedReads(t *testing.T) {
	for _, snapshot := range []rpc.AccountQuotaSnapshot{
		{EntitlementRequests: 50000, UsedRequests: 100, RemainingPercentage: 99.8},
		{EntitlementRequests: -1, IsUnlimitedEntitlement: true, RemainingPercentage: 100},
		{EntitlementRequests: 50000, UsedRequests: 50000, OverageAllowedWithExhaustedQuota: true},
		{EntitlementRequests: 50000, UsedRequests: -1},
		{EntitlementRequests: 50000, RemainingPercentage: math.NaN()},
	} {
		result := mapQuota(runtime.QuotaSnapshot{}, snapshot)
		if snapshot.UsedRequests < 0 || math.IsNaN(snapshot.RemainingPercentage) {
			if result.State != "unavailable" {
				t.Fatal("invalid metrics became available")
			}
		} else if result.State != "available" || result.ExecutionDenied != (snapshot.UsedRequests == 50000) {
			t.Fatal("quota or paid overage mapping was wrong", result)
		}
	}
	adapter, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if _, err := adapter.ReadAccountQuota(t.Context(), "fixture"); err == nil || adapter.started {
		t.Fatal("unapproved read reached the provider")
	}
}

func TestAccountQuotaSignedInAcceptance(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_QUOTA_ACCEPTANCE") != "1" {
		t.Skip("explicit read-only signed-in acceptance")
	}
	account, cli, base := os.Getenv("TAXION_QUOTA_ACCOUNT"), os.Getenv("TAXION_QUOTA_CLI"), os.Getenv("TAXION_QUOTA_BASE_DIRECTORY")
	if account == "" || cli == "" || base == "" {
		t.Fatal("acceptance requires explicit selected account, CLI and state directory")
	}
	policy, err := core.NewBudgetGuidance("", "account")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(Config{Role: "ORCHESTRATOR", SystemPrompt: "Read-only quota acceptance; no model turn",
		WorkingDirectory: t.TempDir(), CLIPath: cli, BaseDirectory: base,
		Configuration: quotaTestSelection(t, account), BudgetGuidance: policy})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	hash := adapter.ConfigHash()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := adapter.ReadAccountQuota(ctx, account)
	if err != nil || result.State != "available" || result.Account != account ||
		result.Entitlement == nil || result.Used == nil || result.RemainingPercentage == nil ||
		len(adapter.sessions) != 0 || adapter.ConfigHash() != hash {
		t.Fatal("selected-account read-only acceptance failed", result.State, err)
	}
}
