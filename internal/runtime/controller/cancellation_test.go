package controller

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/store"
)

func TestPublicationCancellationAndImmutableAudit(t *testing.T) {
	db, ledger, _ := reviewFlowFixture(t)
	proposal := publicationProposal(t)
	hash, err := PublicationHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, publicationScanner(t)); err != nil {
		t.Fatal(err)
	}
	request := store.PublicationCancellation{ID: proposal.ID, PreviousState: "PENDING", ApprovalSource: proposal.ApprovalSource, ContentHash: hash, Reason: "Superseded policy requires new text.", Actor: "human", Authorization: "human:cancel-exact-intent"}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes := cancellationStoreBytes(t, ledger)
	for _, change := range []func(*store.PublicationCancellation){
		func(r *store.PublicationCancellation) { r.ContentHash = "wrong" },
		func(r *store.PublicationCancellation) { r.Authorization = "" },
		func(r *store.PublicationCancellation) { r.ApprovalSource = "different" },
	} {
		bad := request
		change(&bad)
		if _, err := CancelPublication(db, bad); err == nil {
			t.Fatal("invalid cancellation accepted")
		}
		after, err := db.LoadWorkspace()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("rejection changed store", err)
		}
		if !reflect.DeepEqual(beforeBytes, cancellationStoreBytes(t, ledger)) {
			t.Fatal("rejection changed database, WAL or SHM bytes")
		}
	}
	profilePath := os.Getenv("TAXION_COMMUNICATION_PROFILES")
	t.Setenv("TAXION_COMMUNICATION_PROFILES", "")
	if saved, err := CancelPublication(db, request); err != nil || saved.Status != "CANCELED" {
		t.Fatal(saved, err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", profilePath)
	canceled, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if len(canceled.Records.Cancellations) != 1 || canceled.Records.Cancellations[0].At.IsZero() {
		t.Fatal("missing durable cancellation")
	}
	canceledBytes := cancellationStoreBytes(t, ledger)
	if _, err := CancelPublication(db, request); err != nil {
		t.Fatal("idempotent cancellation failed", err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(canceled, after) {
		t.Fatal("idempotent retry changed store", err)
	}
	if !reflect.DeepEqual(canceledBytes, cancellationStoreBytes(t, ledger)) {
		t.Fatal("idempotent retry changed store bytes")
	}
	publisher := &publicationFixture{}
	if _, err := DeliverPublication(t.Context(), db, proposal.ID, publicationScanner(t), publisher); err == nil || publisher.sends != 0 {
		t.Fatal("canceled publication sent")
	}
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, publicationScanner(t)); err == nil {
		t.Fatal("canceled ID reused")
	}
	for _, change := range []func(*store.Workspace){
		func(s *store.Workspace) { s.Records.Cancellations = nil },
		func(s *store.Workspace) { s.Records.Cancellations[0].Reason = "Changed" },
		func(s *store.Workspace) { s.Records.Updates[0].Body = "Changed" },
		func(s *store.Workspace) { s.Records.Updates[0].Status = "PENDING" },
		func(s *store.Workspace) { s.Records.Updates = nil },
	} {
		if err := db.UpdateWorkspace(func(s *store.Workspace) error { change(s); return nil }); err == nil {
			t.Fatal("canceled record or append-only audit changed")
		}
		after, err := db.LoadWorkspace()
		if err != nil || !reflect.DeepEqual(canceled, after) {
			t.Fatal("rejected change persisted", err)
		}
		if !reflect.DeepEqual(canceledBytes, cancellationStoreBytes(t, ledger)) {
			t.Fatal("rejected modification changed store bytes")
		}
	}
}

func cancellationStoreBytes(t *testing.T, ledger string) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := filepath.Join(filepath.Dir(ledger), "workspace.db") + suffix
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		result[suffix] = string(data)
	}
	return result
}

func TestPublicationCancellationRejectsSentOrClaimed(t *testing.T) {
	for _, status := range []string{"SENT", "CLAIMED", "UNKNOWN"} {
		t.Run(status, func(t *testing.T) {
			db, _, _ := reviewFlowFixture(t)
			proposal := publicationProposal(t)
			hash, err := PublicationHash(proposal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, publicationScanner(t)); err != nil {
				t.Fatal(err)
			}
			if err := db.UpdateWorkspace(func(s *store.Workspace) error {
				s.Records.Updates[0].Status = status
				s.Records.Updates[0].Claim = "sender"
				if status == "SENT" {
					s.Records.Updates[0].RemoteID = "123"
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			request := store.PublicationCancellation{ID: proposal.ID, PreviousState: "PENDING", ApprovalSource: proposal.ApprovalSource, ContentHash: hash, Reason: "Cancel", Actor: "human", Authorization: "human:cancel"}
			if _, err := CancelPublication(db, request); err == nil {
				t.Fatal("sent or claimed intent canceled")
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejection changed store", err)
			}
		})
	}
}
