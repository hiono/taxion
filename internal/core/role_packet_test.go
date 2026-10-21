package core

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func rolePacketFields(t *testing.T) RoleHandoffPacketFields {
	t.Helper()
	task := GraphTask{ID: "task-111", BaselineSHA: strings.Repeat("a", 40),
		Scope: []string{"internal/core"}, Acceptance: []string{"packet readback verifies"}}
	dimensions, err := BundleDimensions(task, ApprovalConfiguration{Digest: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := CanonicalSHA256(task)
	if err != nil {
		t.Fatal(err)
	}
	return RoleHandoffPacketFields{
		TaskID: task.ID, TaskHash: hash, BaselineSHA: task.BaselineSHA,
		TargetTreeHash: strings.Repeat("c", 40),
		ScopeDigest:    dimensions.Scope, AcceptanceDigest: dimensions.Acceptance,
		ProhibitedActions: []string{"Do not merge", "Do not publish"},
		Role:              "CODER", RequiredOutputSchema: "taxion.dispatch-result/v1",
	}
}

func TestRolePacketCanonicalImmutableReadback(t *testing.T) {
	fields := rolePacketFields(t)
	packet, err := NewRoleHandoffPacket(fields)
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewRoleHandoffPacket(fields)
	if err != nil || again.Hash() != packet.Hash() {
		t.Fatalf("unstable packet hash: %v", err)
	}
	raw, err := packet.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalJSON(json.RawMessage(raw))
	if err != nil || !bytes.Equal(raw, canonical) {
		t.Fatalf("noncanonical packet: %s: %v", raw, err)
	}
	var reordered bytes.Buffer
	if err := json.Indent(&reordered, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	read, err := ReadRoleHandoffPacket(reordered.Bytes(), packet.Hash())
	if err != nil || !reflect.DeepEqual(read.Readback(), fields) {
		t.Fatalf("readback differs: %+v: %v", read.Readback(), err)
	}
	fields.ProhibitedActions[0] = "mutated input"
	copy := packet.Readback()
	copy.ProhibitedActions[1] = "mutated readback"
	after, err := packet.MarshalJSON()
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("packet was mutable: %s: %v", after, err)
	}
	if err := packet.Verify(strings.Repeat("d", 64)); err == nil {
		t.Fatal("accepted a different receiver hash")
	}
}

func TestRolePacketEveryFieldIsHashBound(t *testing.T) {
	packet, err := NewRoleHandoffPacket(rolePacketFields(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := packet.MarshalJSON()
	var original map[string]json.RawMessage
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if len(original) != 12 {
		t.Fatalf("unexpected packet fields: %v", original)
	}
	for key := range original {
		t.Run(key, func(t *testing.T) {
			var tampered map[string]any
			if err := json.Unmarshal(raw, &tampered); err != nil {
				t.Fatal(err)
			}
			switch key {
			case "packetVersion":
				tampered[key] = 2
			case "role":
				tampered[key] = "REVIEWER"
			case "prohibitedActions":
				tampered[key] = []string{"Do not push"}
			case "taskId":
				tampered[key] = "other-task"
			default:
				tampered[key] = strings.Repeat("e", 64)
			}
			changed, _ := json.Marshal(tampered)
			if _, err := ReadRoleHandoffPacket(changed, packet.Hash()); err == nil {
				t.Fatal("accepted tampering")
			}
			delete(tampered, key)
			missing, _ := json.Marshal(tampered)
			if _, err := ReadRoleHandoffPacket(missing, packet.Hash()); err == nil {
				t.Fatal("accepted missing field")
			}
		})
	}
	replacement := rolePacketFields(t)
	replacement.TaskID = "different-task"
	other, err := NewRoleHandoffPacket(replacement)
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := other.MarshalJSON()
	if _, err := ReadRoleHandoffPacket(changed, packet.Hash()); err == nil {
		t.Fatal("accepted rehashed replacement against receiver pin")
	}
}

func TestRolePacketRejectsForbiddenAndAmbiguousJSON(t *testing.T) {
	packet, _ := NewRoleHandoffPacket(rolePacketFields(t))
	raw, _ := packet.MarshalJSON()
	for _, extra := range []string{
		`"conversationHistory":[]`, `"privateReasoning":"hidden"`, `"contract":{}`,
		`"messages":[]`, `"diff":"inline"`, `"TaskId":"case-alias"`,
		`"role":"CODER"`, `"r\u006fle":"CODER"`,
	} {
		t.Run(extra, func(t *testing.T) {
			changed := append([]byte("{"+extra+","), raw[1:]...)
			if _, err := ReadRoleHandoffPacket(changed, packet.Hash()); err == nil {
				t.Fatal("accepted forbidden or duplicate field")
			}
		})
	}
	for _, malformed := range [][]byte{
		nil, []byte("null"), []byte("[]"), raw[:len(raw)-1],
		append(slices.Clone(raw), []byte(` {}`)...),
		bytes.Replace(raw, []byte(`"previousEvidenceHash":""`), []byte(`"previousEvidenceHash":null`), 1),
		bytes.Replace(raw, []byte(`"prohibitedActions":["Do not merge","Do not publish"]`), []byte(`"prohibitedActions":{}`), 1),
	} {
		if _, err := ReadRoleHandoffPacket(malformed, packet.Hash()); err == nil {
			t.Fatalf("accepted malformed input: %s", malformed)
		}
	}
}

func TestRolePacketCredentialsRedactedBeforeHashing(t *testing.T) {
	secrets := []string{
		"ghp_" + strings.Repeat("x", 30),
		"github_pat_" + strings.Repeat("x", 30),
		"glpat-" + strings.Repeat("x", 30),
		"Bearer " + strings.Repeat("x", 30),
		strings.Join([]string{"-----BEGIN ", "PRIVATE KEY-----\nsensitive\n-----END ", "PRIVATE KEY-----"}, ""),
	}
	for _, secret := range secrets {
		fields := rolePacketFields(t)
		fields.ProhibitedActions = []string{"Do not use " + secret}
		packet, err := NewRoleHandoffPacket(fields)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := packet.MarshalJSON()
		if strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "[REDACTED]") {
			t.Fatal("packet was not redacted")
		}
		fields.ProhibitedActions = []string{"Do not use [REDACTED]"}
		clean, _ := NewRoleHandoffPacket(fields)
		if clean.Hash() != packet.Hash() {
			t.Fatal("redaction happened after hashing")
		}
		fields.TaskID = secret
		if _, err := NewRoleHandoffPacket(fields); err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("credential identity accepted or echoed")
		}
		// Readback may not silently redact and accept a rehashed unsafe packet.
		record := packet.record
		record.ProhibitedActions = []string{secret}
		record.PacketHash = ""
		record.PacketHash, _ = CanonicalSHA256(record)
		unsafe, _ := CanonicalJSON(record)
		if _, err := ReadRoleHandoffPacket(unsafe, record.PacketHash); err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("credential-bearing readback accepted or echoed")
		}
	}
}

func TestRolePacketValidatesReferencesAndRoles(t *testing.T) {
	tests := map[string]func(*RoleHandoffPacketFields){
		"empty task":              func(f *RoleHandoffPacketFields) { f.TaskID = "" },
		"history in ID":           func(f *RoleHandoffPacketFields) { f.TaskID = "some conversation text" },
		"short task hash":         func(f *RoleHandoffPacketFields) { f.TaskHash = "abc" },
		"uppercase":               func(f *RoleHandoffPacketFields) { f.ScopeDigest = strings.Repeat("A", 64) },
		"nonhex":                  func(f *RoleHandoffPacketFields) { f.AcceptanceDigest = strings.Repeat("z", 64) },
		"short baseline":          func(f *RoleHandoffPacketFields) { f.BaselineSHA = "abc123" },
		"empty tree":              func(f *RoleHandoffPacketFields) { f.TargetTreeHash = "" },
		"unknown role":            func(f *RoleHandoffPacketFields) { f.Role = "PLANNER" },
		"inline schema":           func(f *RoleHandoffPacketFields) { f.RequiredOutputSchema = `{"history":[]}` },
		"validator schema":        func(f *RoleHandoffPacketFields) { f.Role = "VALIDATOR" },
		"review missing evidence": func(f *RoleHandoffPacketFields) { f.Role = "REVIEWER" },
		"invalid coder evidence":  func(f *RoleHandoffPacketFields) { f.PreviousEvidenceHash = "none" },
		"empty prohibition":       func(f *RoleHandoffPacketFields) { f.ProhibitedActions = []string{""} },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			fields := rolePacketFields(t)
			change(&fields)
			if _, err := NewRoleHandoffPacket(fields); err == nil {
				t.Fatal("accepted invalid packet")
			}
		})
	}
	fields := rolePacketFields(t)
	fields.ProhibitedActions = nil
	fields.BaselineSHA, fields.TargetTreeHash = strings.Repeat("a", 64), strings.Repeat("b", 64)
	packet, err := NewRoleHandoffPacket(fields)
	if err != nil || packet.Readback().ProhibitedActions == nil {
		t.Fatalf("SHA-256 Git hashes or empty array rejected: %v", err)
	}
	var zero RoleHandoffPacket
	if _, err := zero.MarshalJSON(); err == nil {
		t.Fatal("zero packet serialized")
	}
}
