package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/config"
)

func serviceQuota() *rpc.SessionQuotaGetResult {
	acquisition := rpc.ProviderQuotaAcquisitionStatusSucceeded
	kind := rpc.ProviderQuotaObservationKindAccountSnapshot
	return &rpc.SessionQuotaGetResult{ProviderQuotas: []rpc.ProviderQuotaState{{
		Provider:          rpc.ModelProviderRef{Kind: rpc.ModelProviderKindCopilot},
		AccessState:       rpc.ProviderQuotaAccessStateAllowed,
		CapacityState:     rpc.ProviderQuotaCapacityStateAvailable,
		AcquisitionStatus: &acquisition, ObservationKind: &kind,
	}}}
}

func TestServiceAccessUsesProviderVerdictsNotNumericQuotaOrPlan(t *testing.T) {
	for _, test := range []struct {
		name    string
		update  func(*rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult
		allowed bool
	}{
		{"allowed", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult { return q }, true},
		{"zero-balance", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			zero := int64(0)
			q.ProviderQuotas[0].AvailableQuantity = &zero
			return q
		}, true},
		{"unlimited", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].CapacityState = rpc.ProviderQuotaCapacityStateUnlimited
			return q
		}, true},
		{"not-required", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].AccessState = rpc.ProviderQuotaAccessStateNotRequired
			q.ProviderQuotas[0].CapacityState = rpc.ProviderQuotaCapacityStateNotRequired
			return q
		}, true},
		{"not-applicable", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].CapacityState = rpc.ProviderQuotaCapacityStateNotApplicable
			return q
		}, true},
		{"missing", func(*rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult { return nil }, false},
		{"legacy-snapshots-only", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas = nil
			return q
		}, false},
		{"denied", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].AccessState = rpc.ProviderQuotaAccessStateDenied
			positive := int64(100)
			q.ProviderQuotas[0].AvailableQuantity = &positive
			return q
		}, false},
		{"explicit-no-quota", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			no := false
			q.ProviderQuotas[0].HasQuota = &no
			return q
		}, false},
		{"exhausted", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].CapacityState = rpc.ProviderQuotaCapacityStateExhausted
			return q
		}, false},
		{"missing-access", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].AccessState = ""
			return q
		}, false},
		{"unknown-capacity", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].CapacityState = rpc.ProviderQuotaCapacityStateUnknown
			return q
		}, false},
		{"missing-acquisition", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].AcquisitionStatus = nil
			return q
		}, false},
		{"failed-acquisition", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			failed := rpc.ProviderQuotaAcquisitionStatusFailed
			q.ProviderQuotas[0].AcquisitionStatus = &failed
			return q
		}, false},
		{"admission-from-previous-turn", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			kind := rpc.ProviderQuotaObservationKindAdmissionState
			q.ProviderQuotas[0].ObservationKind = &kind
			return q
		}, false},
		{"missing-observation-kind", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].ObservationKind = nil
			return q
		}, false},
		{"other-provider", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			q.ProviderQuotas[0].Provider.Kind = "other"
			return q
		}, false},
		{"conflicting-observations", func(q *rpc.SessionQuotaGetResult) *rpc.SessionQuotaGetResult {
			denied := q.ProviderQuotas[0]
			denied.AccessState = rpc.ProviderQuotaAccessStateDenied
			q.ProviderQuotas = append(q.ProviderQuotas, denied)
			return q
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			quota := test.update(serviceQuota())
			if quota != nil {
				detail := "private-provider-detail"
				for i := range quota.ProviderQuotas {
					quota.ProviderQuotas[i].Reason = &detail
					quota.ProviderQuotas[i].AcquisitionError = &detail
				}
			}
			err := checkServiceAccess(quota)
			if (err == nil) != test.allowed || err != nil && strings.Contains(err.Error(), "private-provider-detail") {
				t.Fatal("service admission inferred from balances or exposed provider details", err)
			}
		})
	}
}

func TestServiceAccessRejectsMissingAndStoppedSessions(t *testing.T) {
	adapter, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if err := adapter.CheckServiceAccess(context.Background(), "missing"); err == nil {
		t.Fatal("missing session accepted")
	}
	adapter.sessions["stopped"] = &managedSession{stopped: true}
	if err := adapter.CheckServiceAccess(context.Background(), "stopped"); !errors.Is(err, ErrStoppedSession) {
		t.Fatal("stopped session accepted", err)
	}
}

func TestServiceAccessUsesSDKSessionQuotaRPCWithoutModelSend(t *testing.T) {
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
			reader := bufio.NewReader(conn)
			var sessionID string
			for _, step := range []struct{ method, field, response string }{
				{"connect", "result", `{"protocolVersion":3}`},
				{"session.create", "result", ""},
				{"session.quota.get", "result", `{"providerQuotas":[{"provider":{"kind":"copilot"},"acquisitionStatus":"succeeded","observationKind":"account_snapshot","accessState":"allowed","capacityState":"available"}]}`},
				{"session.quota.get", "result", `{"providerQuotas":[{"provider":{"kind":"copilot"},"acquisitionStatus":"succeeded","observationKind":"account_snapshot","accessState":"denied","capacityState":"available","reason":"private-provider-detail"}]}`},
				{"session.quota.get", "error", `{"code":-32603,"message":"private-provider-detail"}`},
				{"session.quota.get", "result", `null`},
				{"session.detach", "result", `{"success":true}`},
				{"session.detach", "result", `{"success":true}`},
			} {
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
					Params struct {
						SessionID string `json:"sessionId"`
					} `json:"params"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					return err
				}
				if request.Method != step.method ||
					(step.method == "session.quota.get" || step.method == "session.detach") && request.Params.SessionID != sessionID {
					return fmt.Errorf("unexpected RPC method or session: %s", request.Method)
				}
				if step.method == "session.create" {
					sessionID = request.Params.SessionID
					result, err := json.Marshal(map[string]string{"sessionId": sessionID})
					if err != nil {
						return err
					}
					step.response = string(result)
				}
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"%s":%s}`, request.ID, step.field, step.response)
				if _, err := fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(reply), reply); err != nil {
					return err
				}
			}
			if _, err := reader.Peek(1); !errors.Is(err, io.EOF) {
				return fmt.Errorf("expected client disconnect after service quota responses: %v", err)
			}
			return nil
		}()
	}()
	adapter, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter.client = sdk.NewClient(&sdk.ClientOptions{Connection: sdk.URIConnection{URL: listener.Addr().String()}})
	defer adapter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := adapter.Start(ctx, "test-task")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := adapter.lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := adapter.CheckServiceAccess(cancelled, id); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled query made an RPC", err)
	}
	for i := range 4 {
		handle.mu.Lock()
		handle.busy = i == 0
		handle.mu.Unlock()
		err := adapter.CheckServiceAccess(ctx, id)
		if (err == nil) != (i == 0) || err != nil && strings.Contains(err.Error(), "private-provider-detail") {
			t.Fatal("quota RPC response accepted or leaked incorrectly", err)
		}
		handle.mu.Lock()
		busy := handle.busy
		handle.mu.Unlock()
		if busy != (i == 0) {
			t.Fatal("quota query changed the session's busy state")
		}
	}
	if err := adapter.Close(); err != nil {
		t.Fatal("service quota client cleanup failed", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLiveCopilotServiceAccessWithoutModelCall(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_ACCESS_INTEGRATION") != "1" {
		t.Skip("set TAXION_COPILOT_ACCESS_INTEGRATION=1 to query saved Copilot authentication and service access")
	}
	account := os.Getenv("TAXION_TEST_COPILOT_ACCOUNT")
	if strings.TrimSpace(account) == "" || strings.TrimSpace(account) != account {
		t.Fatal("TAXION_TEST_COPILOT_ACCOUNT must identify the expected saved account")
	}
	selected, err := config.Load([]byte(`version: 1
project: {id: integration}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: gh:active}
runtime: {provider: copilot, account: ai-user, credentialRef: copilot:logged-in, contractRef: integration}
execution: {requester: requester, worker: local, approvalRef: integration}
`))
	if err != nil {
		t.Fatal(err)
	}
	selected.Runtime.Account = account
	cfg := testConfig(t)
	cfg.CLIPath, cfg.BaseDirectory = "", ""
	cfg.MaxAICredits = 30
	cfg.Configuration, err = json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(cfg)
	if err != nil {
		t.Fatal("live adapter setup failed; details withheld")
	}
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Error("live adapter cleanup failed; details withheld")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := adapter.CheckAuthentication(ctx, account); err != nil {
		t.Fatal(err)
	}
	id, err := adapter.Start(ctx, "service-access-integration")
	if err != nil {
		t.Fatal("live model-free session creation failed; details withheld")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := adapter.Stop(ctx, id); err != nil {
			t.Error("live model-free session cleanup failed; details withheld")
		}
	})
	for range 2 {
		if err := adapter.CheckServiceAccess(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	handle, err := adapter.lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := handle.sdk.RPC.Usage.GetMetrics(ctx)
	if err != nil {
		t.Fatal("live model-free usage verification failed; details withheld")
	}
	if metrics == nil || metrics.TotalUserRequests != 0 || len(metrics.ModelMetrics) != 0 || len(metrics.ProviderModelMetrics) != 0 {
		t.Fatal("service access check unexpectedly generated model usage")
	}
}
