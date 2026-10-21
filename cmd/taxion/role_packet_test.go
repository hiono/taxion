package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func packetCreateArgs(role, previous string) []string {
	args := []string{"create", "--task", "task-111", "--task-hash", strings.Repeat("a", 64),
		"--baseline-sha", strings.Repeat("b", 40), "--target-tree-hash", strings.Repeat("c", 40),
		"--scope-digest", strings.Repeat("d", 64), "--acceptance-digest", strings.Repeat("e", 64),
		"--role", role, "--prohibit", "Do not merge", "--prohibit", "Do not publish"}
	if previous != "" {
		args = append(args, "--previous-evidence-hash", previous)
	}
	return args
}

func TestRolePacketCoderValidatorReviewerReadback(t *testing.T) {
	directory := t.TempDir()
	previousStore := storePath
	storePath = filepath.Join(directory, "absent", "ledger.json")
	t.Cleanup(func() { storePath = previousStore })
	coderEvidence, err := core.CanonicalSHA256(core.DispatchResult{
		DispatchID: "coder-1", TaskID: "task-111", Recipient: "CODER", OutputTreeSHA: strings.Repeat("c", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Existing evidence digests remain separate from packet hashes.
	validationEvidence, err := core.CanonicalSHA256(map[string]string{"qualityGate": "RECORDED_PASS"})
	if err != nil {
		t.Fatal(err)
	}
	report := controller.GateReport{Version: controller.GateReportVersion,
		Gate: controller.GateExecution, TaskID: "task-111",
		Hashes: controller.GateHashes{Evidence: validationEvidence}}
	hashes := map[string]bool{}
	for _, turn := range []struct{ role, previous, schema string }{
		{"CODER", "", "taxion.dispatch-result/v1"},
		{"VALIDATOR", coderEvidence, "taxion.gate-report/v1"},
		{"REVIEWER", report.Hashes.Evidence, "taxion.dispatch-result/v1"},
	} {
		var created bytes.Buffer
		if err := rolePacketCommand(packetCreateArgs(turn.role, turn.previous), &created); err != nil {
			t.Fatal(err)
		}
		// Record emitted bytes and the receiver pin separately.
		var record struct {
			Hash string `json:"packetHash"`
		}
		if err := json.Unmarshal(created.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		hash := record.Hash
		if turn.role == "CODER" && hash != "5957547dae8737e261035098cc3d4d7a7117073bacc833678044d79c7ad061aa" {
			t.Fatalf("canonical hash changed: %s", hash)
		}
		packet, err := core.ReadRoleHandoffPacket(created.Bytes(), hash)
		if err != nil {
			t.Fatal(err)
		}
		fields := packet.Readback()
		if fields.Role != turn.role || fields.PreviousEvidenceHash != turn.previous ||
			fields.RequiredOutputSchema != turn.schema || hashes[hash] {
			t.Fatalf("incorrect handoff references: %+v", fields)
		}
		hashes[hash] = true
		path := filepath.Join(directory, turn.role+".json")
		if err := os.WriteFile(path, created.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		var readback bytes.Buffer
		if err := rolePacketCommand([]string{"verify", "--file", path, "--expected-hash", hash}, &readback); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(created.Bytes(), readback.Bytes()) {
			t.Fatal("CLI readback changed the packet")
		}
		if err := rolePacketCommand([]string{"verify", "--file", path, "--expected-hash", strings.Repeat("f", 64)}, io.Discard); err == nil {
			t.Fatal("accepted wrong receiver pin")
		}
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
		t.Fatalf("packet operation created workspace state: %v", err)
	}
}

func TestRolePacketCommandRejectsInvalidInputsWithoutLeaking(t *testing.T) {
	secret := "ghp_" + strings.Repeat("x", 30)
	for _, args := range [][]string{
		nil, {"unknown"}, {"verify"}, {"verify", "--file", "missing", "--expected-hash", secret},
		{"create"}, {"create", "extra"}, {"create", "--" + secret},
		{"create", "--task"}, packetCreateArgs("VALIDATOR", ""),
	} {
		var output bytes.Buffer
		err := rolePacketCommand(args, &output)
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(output.String(), secret) {
			t.Fatalf("invalid input accepted or leaked: %v", err)
		}
	}
	for _, action := range []string{"create", "verify"} {
		var output bytes.Buffer
		if err := rolePacketCommand([]string{action, "--help"}, &output); err != nil ||
			!strings.Contains(output.String(), "Usage of role-packet "+action) {
			t.Fatalf("flag help missing: %s: %v", output.String(), err)
		}
	}
	args := append(packetCreateArgs("CODER", ""), "--prohibit", "Do not use "+secret)
	var output bytes.Buffer
	if err := rolePacketCommand(args, &output); err != nil || strings.Contains(output.String(), secret) ||
		!strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("CLI redaction failed: %v", err)
	}
	if err := rolePacketCommand(packetCreateArgs("CODER", ""), failingPacketWriter{}); err == nil {
		t.Fatal("ignored output failure")
	}
}

type failingPacketWriter struct{}

func (failingPacketWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
