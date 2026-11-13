package store

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRecordsRejectCorruptAndIncompatibleState(t *testing.T) {
	for _, data := range []string{`{`, `null`, `{} {}`, `{"version":999}`, `{"events":[{}]}`,
		`{"updates":[{"id":"report","status":"SUCCESS"}]}`, `{"findings":[{}]}`} {
		t.Run(data, func(t *testing.T) {
			db := openTest(t)
			if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.Exec(`UPDATE workspace_records SET records_json=? WHERE id=1`, data); err != nil {
				t.Fatal(err)
			}
			if _, err := db.LoadWorkspace(); err == nil {
				t.Fatal("invalid stored records accepted")
			}
			if err := db.UpdateWorkspace(func(*Workspace) error { return nil }); err == nil {
				t.Fatal("corrupt records overwritten")
			}
		})
	}
}

func TestRecordsShareInitializationAndUpdateTransaction(t *testing.T) {
	db := openTest(t)
	if _, err := db.db.Exec(`CREATE TRIGGER reject_records BEFORE INSERT ON workspace_records BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.InitializeWorkspace(Workspace{}); err == nil {
		t.Fatal("records save failure ignored")
	}
	if initialized, err := db.WorkspaceInitialized(); err != nil || initialized {
		t.Fatal("partial workspace initialization", err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER reject_records`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	update := ExternalUpdate{ID: "report", TaskID: "task", Hash: strings.Repeat("a", 64),
		Actor: "human", ApprovalSource: "human:report", Status: "PENDING", At: time.Now().UTC()}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Records.Updates = append(state.Records.Updates, update)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Records.Updates[0].Status = "invented"
		return nil
	}); err == nil {
		t.Fatal("unknown status accepted")
	}
	state, err := db.LoadWorkspace()
	if err != nil || state.Records.Updates[0].Status != "PENDING" {
		t.Fatal("invalid update persisted", err)
	}
}

func TestBudgetAuditRecordsAreAppendOnly(t *testing.T) {
	db := openTest(t)
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	payload := `{"remaining":10}`
	record := BudgetAudit{
		FlowID: "flow", TaskID: "task", Sequence: 1, Kind: "USAGE_OBSERVED",
		Payload: payload, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(payload))), At: time.Now().UTC(),
	}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Records.Budgets = append(state.Records.Budgets, record)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Workspace){
		func(state *Workspace) { state.Records.Budgets = nil },
		func(state *Workspace) { state.Records.Budgets[0].Payload = `{"remaining":0}` },
		func(state *Workspace) { state.Records.Budgets[0].Hash = strings.Repeat("a", 64) },
	} {
		if err := db.UpdateWorkspace(func(state *Workspace) error {
			mutate(state)
			return nil
		}); err == nil {
			t.Fatal("budget audit mutation accepted")
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Budgets) != 1 || state.Records.Budgets[0] != record {
		t.Fatalf("budget audit history changed: %+v, %v", state.Records.Budgets, err)
	}
}
