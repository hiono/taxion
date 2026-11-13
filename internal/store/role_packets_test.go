package store

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func storedPacketFixture(t *testing.T) (core.RoleHandoffPacket, PacketAuthorization, PacketAudit) {
	t.Helper()
	auth := PacketAuthorization{
		Version: 1, TaskID: "ready", TaskHash: strings.Repeat("a", 64),
		ProposalHash: "sha256:" + strings.Repeat("b", 64), BundleHash: strings.Repeat("c", 64),
		BaselineSHA: strings.Repeat("d", 40), InitialTree: strings.Repeat("e", 40),
		ScopeDigest: strings.Repeat("f", 64), Acceptance: strings.Repeat("1", 64),
		ReviewerPolicy: "same-contract-reviewer/v1", Actor: "human", Source: "human:1", Reason: "recover artifacts",
		Unavailable: []UnavailablePacket{{Role: "CODER", Hash: strings.Repeat("2", 64), Evidence: "artifact was not preserved"}},
	}
	var err error
	auth.Hash, err = core.CanonicalSHA256(auth)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := core.NewRoleHandoffPacket(core.RoleHandoffPacketFields{
		TaskID: auth.TaskID, TaskHash: auth.TaskHash, BaselineSHA: auth.BaselineSHA, TargetTreeHash: auth.InitialTree,
		ScopeDigest: auth.ScopeDigest, AcceptanceDigest: auth.Acceptance, Role: "CODER",
		RequiredOutputSchema: "taxion.dispatch-result/v1", ProhibitedActions: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	audit := PacketAudit{Version: 1, AuthorizationHash: auth.Hash, TaskID: auth.TaskID,
		DispatchID: "coder", InstructionHash: strings.Repeat("3", 64), Role: "CODER",
		PacketHash: packet.Hash(), At: time.Now().UTC()}
	return packet, auth, audit
}

func TestPacketStoreRoundTripAndAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packet, auth, audit := storedPacketFixture(t)
	state := Workspace{Records: Records{PacketAuthorizations: []PacketAuthorization{auth}, PacketAudits: []PacketAudit{audit}}}
	if err := state.Records.SavePacket(packet); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(state); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(s *Workspace) error { return s.Records.SavePacket(packet) }); err != nil {
		t.Fatal(err)
	}
	before, err := db.LoadWorkspace()
	if err != nil || len(before.Records.Packets) != 1 {
		t.Fatalf("idempotent persistence failed: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*Workspace)
	}{
		{"delete-packet", func(s *Workspace) { s.Records.Packets = nil }},
		{"replace-packet", func(s *Workspace) { s.Records.Packets[0].Canonical += " " }},
		{"delete-authorization", func(s *Workspace) { s.Records.PacketAuthorizations = nil }},
		{"replace-authorization", func(s *Workspace) { s.Records.PacketAuthorizations[0].Reason += " changed" }},
		{"mutate-nested-unavailable", func(s *Workspace) { s.Records.PacketAuthorizations[0].Unavailable[0].Evidence += " changed" }},
		{"delete-audit", func(s *Workspace) { s.Records.PacketAudits = nil }},
		{"replace-audit", func(s *Workspace) { s.Records.PacketAudits[0].PacketHash = strings.Repeat("a", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := db.UpdateWorkspace(func(s *Workspace) error { test.edit(s); return nil }); err == nil {
				t.Fatal("immutable packet data changed")
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed update was not rolled back: %v", err)
			}
		})
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	readback, err := reader.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := readback.Records.ReadPacket(packet.Hash())
	data, marshalErr := recovered.MarshalJSON()
	if err != nil || marshalErr != nil || string(data) != before.Records.Packets[0].Canonical {
		t.Fatalf("canonical packet readback failed: %v %v", err, marshalErr)
	}
}

func TestPacketStoreRejectsCorruptionAndUnknownVersions(t *testing.T) {
	packet, auth, audit := storedPacketFixture(t)
	good := Records{PacketAuthorizations: []PacketAuthorization{auth}, PacketAudits: []PacketAudit{audit}}
	if err := good.SavePacket(packet); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*Records)
	}{
		{"missing-artifact", func(r *Records) { r.Packets = nil }},
		{"tampered", func(r *Records) {
			r.Packets[0].Canonical = strings.Replace(r.Packets[0].Canonical, `"role":"CODER"`, `"role":"VALIDATOR"`, 1)
		}},
		{"future-packet", func(r *Records) {
			r.Packets[0].Canonical = strings.Replace(r.Packets[0].Canonical, `"packetVersion":1`, `"packetVersion":99`, 1)
		}},
		{"noncanonical", func(r *Records) { r.Packets[0].Canonical += "\n" }},
		{"future-authorization", func(r *Records) { r.PacketAuthorizations[0].Version = 99 }},
		{"future-audit", func(r *Records) { r.PacketAudits[0].Version = 99 }},
		{"wrong-task", func(r *Records) { r.PacketAudits[0].TaskID = "other" }},
		{"fake-validation", func(r *Records) { r.PacketAudits[0].Validation = `{"passed":true}` }},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openTest(t)
			copy := good
			copy.Packets = append([]PacketArtifact{}, good.Packets...)
			copy.PacketAuthorizations = append([]PacketAuthorization{}, good.PacketAuthorizations...)
			copy.PacketAudits = append([]PacketAudit{}, good.PacketAudits...)
			test.edit(&copy)
			if _, err := db.InitializeWorkspace(Workspace{Records: copy}); err == nil {
				t.Fatal("invalid packet record was persisted")
			}
		})
	}
}
