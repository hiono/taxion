package core

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
)

const RoleHandoffPacketVersion = 1

// RoleHandoffPacketFields contains references only, not contracts or evidence
// bodies. Output schemas name existing Taxion records, not executable schemas.
type RoleHandoffPacketFields struct {
	TaskID               string   `json:"taskId"`
	TaskHash             string   `json:"taskHash"`
	BaselineSHA          string   `json:"baselineSha"`
	TargetTreeHash       string   `json:"targetTreeHash"`
	ScopeDigest          string   `json:"scopeDigest"`
	AcceptanceDigest     string   `json:"acceptanceDigest"`
	ProhibitedActions    []string `json:"prohibitedActions"`
	Role                 string   `json:"role"`
	PreviousEvidenceHash string   `json:"previousEvidenceHash"`
	RequiredOutputSchema string   `json:"requiredOutputSchema"`
}

type rolePacketRecord struct {
	PacketVersion int `json:"packetVersion"`
	RoleHandoffPacketFields
	PacketHash string `json:"packetHash"`
}

// RoleHandoffPacket owns an immutable snapshot. Readback returns detached data;
// it cannot change the bytes or hash of an already created packet.
type RoleHandoffPacket struct {
	record rolePacketRecord
}

// NewRoleHandoffPacket redacts prohibited-action text before hashing. Identity
// and digest fields must already be credential-free. This does not authorize
// execution or assert that referenced evidence passed.
func NewRoleHandoffPacket(fields RoleHandoffPacketFields) (RoleHandoffPacket, error) {
	fields.ProhibitedActions = slices.Clone(fields.ProhibitedActions)
	if fields.ProhibitedActions == nil {
		fields.ProhibitedActions = []string{}
	}
	for i, action := range fields.ProhibitedActions {
		fields.ProhibitedActions[i] = Redact(action)
	}
	packet := RoleHandoffPacket{record: rolePacketRecord{
		PacketVersion: RoleHandoffPacketVersion, RoleHandoffPacketFields: fields,
	}}
	if err := packet.validate(); err != nil {
		return RoleHandoffPacket{}, err
	}
	hash, err := CanonicalSHA256(packet.record)
	if err != nil {
		return RoleHandoffPacket{}, err
	}
	packet.record.PacketHash = hash
	return packet, nil
}

// Readback returns a copy of the reference fields, including a private slice.
func (p RoleHandoffPacket) Readback() RoleHandoffPacketFields {
	fields := p.record.RoleHandoffPacketFields
	fields.ProhibitedActions = slices.Clone(fields.ProhibitedActions)
	return fields
}

func (p RoleHandoffPacket) Hash() string { return p.record.PacketHash }

// MarshalJSON uses the same canonical serialization as approval bundles.
func (p RoleHandoffPacket) MarshalJSON() ([]byte, error) {
	if err := p.Verify(p.Hash()); err != nil {
		return nil, err
	}
	return CanonicalJSON(p.record)
}

// Verify checks both the content hash and the receiver's independently recorded
// expected hash. A self-consistent replacement packet does not satisfy the pin.
func (p RoleHandoffPacket) Verify(expectedHash string) error {
	if err := p.validate(); err != nil {
		return err
	}
	if !rolePacketHash(expectedHash, 64) || p.record.PacketHash != expectedHash {
		return fmt.Errorf("taxion: role packet expected hash mismatch")
	}
	record := p.record
	record.PacketHash = ""
	hash, err := CanonicalSHA256(record)
	if err != nil {
		return err
	}
	if hash != expectedHash {
		return fmt.Errorf("taxion: role packet content hash mismatch")
	}
	return nil
}

// ReadRoleHandoffPacket rejects extra, missing, duplicate and differently cased
// fields before verification. Diagnostics never echo untrusted packet content.
func ReadRoleHandoffPacket(data []byte, expectedHash string) (RoleHandoffPacket, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return RoleHandoffPacket{}, fmt.Errorf("taxion: role packet requires a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return RoleHandoffPacket{}, fmt.Errorf("taxion: malformed role packet field")
		}
		name, ok := key.(string)
		if !ok {
			return RoleHandoffPacket{}, fmt.Errorf("taxion: malformed role packet field")
		}
		switch name {
		case "packetVersion", "taskId", "taskHash", "baselineSha", "targetTreeHash",
			"scopeDigest", "acceptanceDigest", "prohibitedActions", "role",
			"previousEvidenceHash", "requiredOutputSchema", "packetHash":
		default:
			return RoleHandoffPacket{}, fmt.Errorf("taxion: forbidden role packet field")
		}
		if _, exists := fields[name]; exists {
			return RoleHandoffPacket{}, fmt.Errorf("taxion: duplicate role packet field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return RoleHandoffPacket{}, fmt.Errorf("taxion: invalid role packet value")
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return RoleHandoffPacket{}, fmt.Errorf("taxion: malformed role packet object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || len(fields) != 12 {
		return RoleHandoffPacket{}, fmt.Errorf("taxion: role packet requires exactly the versioned fields")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return RoleHandoffPacket{}, err
	}
	var packet RoleHandoffPacket
	if err := json.Unmarshal(raw, &packet.record); err != nil {
		return RoleHandoffPacket{}, fmt.Errorf("taxion: invalid role packet field type")
	}
	if err := packet.Verify(expectedHash); err != nil {
		return RoleHandoffPacket{}, err
	}
	return packet, nil
}

func (p RoleHandoffPacket) validate() error {
	r := p.record
	if r.PacketVersion != RoleHandoffPacketVersion {
		return fmt.Errorf("taxion: unsupported role packet version")
	}
	if r.TaskID == "" || credentialLike(r.TaskID) ||
		strings.ContainsFunc(r.TaskID, func(c rune) bool {
			return !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("._-", c)
		}) {
		return fmt.Errorf("taxion: role packet requires a credential-free Task ID")
	}
	for _, hash := range []string{r.TaskHash, r.ScopeDigest, r.AcceptanceDigest} {
		if !rolePacketHash(hash, 64) {
			return fmt.Errorf("taxion: role packet requires lowercase SHA-256 digests")
		}
	}
	for _, hash := range []string{r.BaselineSHA, r.TargetTreeHash} {
		if !rolePacketHash(hash, 40) && !rolePacketHash(hash, 64) {
			return fmt.Errorf("taxion: role packet requires full lowercase Git object hashes")
		}
	}
	switch r.Role {
	case "CODER", "REVIEWER":
		if r.RequiredOutputSchema != "taxion.dispatch-result/v1" {
			return fmt.Errorf("taxion: role packet requires the DispatchResult schema")
		}
	case "VALIDATOR":
		if r.RequiredOutputSchema != "taxion.gate-report/v1" {
			return fmt.Errorf("taxion: role packet requires the GateReport v1 schema")
		}
	default:
		return fmt.Errorf("taxion: unsupported role packet role")
	}
	if r.PreviousEvidenceHash != "" || r.Role != "CODER" {
		if !rolePacketHash(r.PreviousEvidenceHash, 64) {
			return fmt.Errorf("taxion: role packet requires the previous evidence SHA-256")
		}
	}
	if r.ProhibitedActions == nil {
		return fmt.Errorf("taxion: role packet requires a prohibitedActions array")
	}
	for _, action := range r.ProhibitedActions {
		if strings.TrimSpace(action) == "" || credentialLike(action) {
			return fmt.Errorf("taxion: role packet prohibited action is empty or contains credentials")
		}
	}
	return nil
}

func rolePacketHash(value string, size int) bool {
	if len(value) != size || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
