package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceDocumentsOnlyWriteChangedRecords(t *testing.T) {
	db := openTest(t)
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TABLE writes(area TEXT, field TEXT);
		CREATE TRIGGER document_insert AFTER INSERT ON workspace_documents
		BEGIN INSERT INTO writes VALUES(new.area,new.field); END;
		CREATE TRIGGER document_update AFTER UPDATE ON workspace_documents
		BEGIN INSERT INTO writes VALUES(new.area,new.field); END;
		CREATE TRIGGER document_delete AFTER DELETE ON workspace_documents
		BEGIN INSERT INTO writes VALUES(old.area,old.field); END;
		CREATE TRIGGER reject_graph_blob BEFORE UPDATE OF graph_json,ledger_json ON workspace_state
		BEGIN SELECT RAISE(ABORT,'aggregate rewrite'); END;`); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		if _, err := db.db.Exec(`DELETE FROM writes`); err != nil {
			t.Fatal(err)
		}
		receipt := RuntimeReceipt{DispatchID: "dispatch", SessionID: "session", StreamID: "stream",
			Sequence: uint64(index + 1), Type: "usage", Payload: "fixture", Hash: strings.Repeat("a", 64), At: time.Now().UTC()}
		if err := db.UpdateWorkspace(func(state *Workspace) error {
			state.Records.Events = append(state.Records.Events, receipt)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.db.QueryRow(`SELECT count(*) FROM writes`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("event append wrote %d records: %v", count, err)
		}
		state, err := db.LoadWorkspace()
		if err != nil || len(state.Records.Events) != index+1 || !reflect.DeepEqual(state.Records.Events[index], receipt) {
			t.Fatal("event changed during round trip", err)
		}
	}
}

func TestLegacyWorkspaceMigrationPreservesStateAndRollsBack(t *testing.T) {
	db := openTest(t)
	want := Workspace{Records: Records{Events: []RuntimeReceipt{{
		DispatchID: "dispatch", SessionID: "session", StreamID: "stream", Sequence: 1,
		Type: "usage", Payload: "legacy payload", Hash: strings.Repeat("b", 64), At: time.Now().UTC(),
	}}}}
	ledger, _ := json.Marshal(want.Ledger)
	graph, _ := json.Marshal(want.Graph)
	records, _ := json.Marshal(want.Records)
	if _, err := db.db.Exec(`INSERT INTO workspace_state VALUES(1,37,?,?,0)`, string(ledger), string(graph)); err != nil {
		t.Fatal(err)
	}

	if _, err := db.db.Exec(`INSERT INTO workspace_records VALUES(1,?)`, string(records)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER reject_migration BEFORE UPDATE ON workspace_state
		BEGIN SELECT RAISE(ABORT,'fixture'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateDocuments(); err == nil {
		t.Fatal("failed migration succeeded")
	}
	var count int
	if err := db.db.QueryRow(`SELECT count(*) FROM workspace_documents`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial records persisted", count, err)
	}
	before, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, want) {
		t.Fatal("failed migration changed old state", err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER reject_migration`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrateDocuments(); err != nil {
		t.Fatal(err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(after, want) {
		t.Fatal("migration changed existing state", err)
	}
	var revision int
	if err := db.db.QueryRow(`SELECT revision FROM workspace_state`).Scan(&revision); err != nil || revision != 37 {
		t.Fatal("migration changed the logical revision", err)
	}
	if _, err := db.db.Exec(`UPDATE workspace_documents SET content='"tampered"' WHERE area='records'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoadWorkspace(); err == nil {
		t.Fatal("tampered normalized record accepted")
	}
}

func TestDocumentStringPoolDeduplicatesWithoutReencoding(t *testing.T) {
	db := openTest(t)
	tx, err := db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	token, err := json.Marshal(strings.Repeat(`quoted \" value 文`, 100))
	if err != nil {
		t.Fatal(err)
	}
	original := `{"z":9007199254740993, "a":` + string(token) + `, "b":` + string(token) + `, "raw":{"z":1,"a":2}}`
	content, refs, err := poolDocumentStrings(tx, original)
	if err != nil || len(content)+len(refs) >= len(original) {
		t.Fatal("long strings not reduced", err)
	}
	pool, err := loadDocumentStrings(tx)
	if err != nil || len(pool) != 1 {
		t.Fatal("same string stored more than once", len(pool), err)
	}
	restored, err := expandDocumentStrings(content, refs, pool)
	if err != nil || restored != original {
		t.Fatal("pool changed key order, whitespace, escaping or numeric precision", err)
	}
	for _, broken := range []string{`[{"offset":9223372036854775807,"hash":"missing"}]`, `[{"offset":-1,"hash":"missing"}]`, `{}`} {
		if _, err := expandDocumentStrings(content, broken, pool); err == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	if _, err := tx.Exec(`UPDATE workspace_strings SET content='"tampered"'`); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDocumentStrings(tx); err == nil {
		t.Fatal("tampered interned string accepted")
	}
}
