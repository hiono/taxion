package store

import (
	"strings"
	"testing"
)

func TestRequestRecordsPreservePayloadAndRollbackSequence(t *testing.T) {
	db := openTest(t)
	for _, item := range []struct{ kind, payload string }{
		{"planning", `{"input":"approved request"}`},
		{"execution", `{"flowId":"flow","error":"observed failure"}`},
		{"execution", `{"flowId":"flow","error":"retry explicitly approved"}`},
	} {
		if err := db.AppendRequestRecord("request", item.kind, item.payload); err != nil {
			t.Fatal(err)
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Requests) != 3 {
		t.Fatal("request receipts missing", err)
	}
	last := state.Records.Requests[2]
	if last.Payload != `{"flowId":"flow","error":"retry explicitly approved"}` || last.Sequence != 2 {
		t.Fatal("execution summary lost content or sequence", last)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER reject_request BEFORE INSERT ON workspace_documents
		WHEN new.area='records' AND new.field='requests'
		BEGIN SELECT RAISE(ABORT,'fixture'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendRequestRecord("request", "execution", `{"error":"`+strings.Repeat("not persisted", 200)+`"}`); err == nil {
		t.Fatal("failed request save succeeded")
	}
	after, err := db.LoadWorkspace()
	if err != nil || len(after.Records.Requests) != 3 {
		t.Fatal("failed save left a partial request record", err)
	}
	if err := db.AppendRequestRecord("request", "execution", "not JSON"); err == nil {
		t.Fatal("invalid request payload accepted")
	}
}
