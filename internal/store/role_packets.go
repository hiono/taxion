package store

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

type PacketArtifact struct {
	Hash      string `json:"hash"`
	Canonical string `json:"canonical"`
}

type UnavailablePacket struct {
	Role     string `json:"role"`
	Hash     string `json:"hash"`
	Evidence string `json:"evidence"`
}

// PacketAuthorization is separate from the Task and its ApprovalBundle.
type PacketAuthorization struct {
	Version        int                 `json:"version"`
	TaskID         string              `json:"taskId"`
	TaskHash       string              `json:"taskHash"`
	ProposalHash   string              `json:"proposalHash"`
	BundleHash     string              `json:"bundleHash"`
	BaselineSHA    string              `json:"baselineSha"`
	InitialTree    string              `json:"initialTree"`
	ScopeDigest    string              `json:"scopeDigest"`
	Acceptance     string              `json:"acceptanceDigest"`
	ReviewerPolicy string              `json:"reviewerApprovalPolicy"`
	Unavailable    []UnavailablePacket `json:"unavailable"`
	Actor          string              `json:"actor"`
	Source         string              `json:"source"`
	Reason         string              `json:"reason"`
	Hash           string              `json:"hash"`
}

type PacketAudit struct {
	Version           int       `json:"version"`
	AuthorizationHash string    `json:"authorizationHash"`
	TaskID            string    `json:"taskId"`
	DispatchID        string    `json:"dispatchId"`
	InstructionHash   string    `json:"instructionHash"`
	Role              string    `json:"role"`
	PacketHash        string    `json:"packetHash"`
	EvidenceHash      string    `json:"evidenceHash,omitempty"`
	Validation        string    `json:"validation,omitempty"`
	At                time.Time `json:"at"`
}

func (a PacketAuthorization) Verify() error {
	copy := a
	copy.Hash = ""
	hash, err := core.CanonicalSHA256(copy)
	if err != nil || hash != a.Hash || a.Version != 1 || a.TaskID == "" ||
		strings.TrimSpace(a.Actor) == "" || strings.TrimSpace(a.Source) == "" || strings.TrimSpace(a.Reason) == "" || !recordHash(a.TaskHash) ||
		!recordHash(a.BundleHash) || !recordHash(a.ScopeDigest) || !recordHash(a.Acceptance) ||
		!recordHash(strings.TrimPrefix(a.ProposalHash, "sha256:")) || a.ReviewerPolicy != "same-contract-reviewer/v1" {
		return fmt.Errorf("invalid packet lifecycle authorization")
	}
	for _, text := range []string{a.TaskID, a.Actor, a.Source, a.Reason} {
		if core.Redact(text) != text {
			return fmt.Errorf("packet lifecycle authorization contains credentials")
		}
	}
	seen := map[string]bool{}
	for _, missing := range a.Unavailable {
		if !slices.Contains([]string{"CODER", "VALIDATOR", "REVIEWER"}, missing.Role) ||
			!recordHash(missing.Hash) || strings.TrimSpace(missing.Evidence) == "" || seen[missing.Role] ||
			core.Redact(missing.Evidence) != missing.Evidence {
			return fmt.Errorf("invalid unavailable packet evidence")
		}
		seen[missing.Role] = true
	}
	_, err = core.NewRoleHandoffPacket(core.RoleHandoffPacketFields{
		TaskID: a.TaskID, TaskHash: a.TaskHash, BaselineSHA: a.BaselineSHA, TargetTreeHash: a.InitialTree,
		ScopeDigest: a.ScopeDigest, AcceptanceDigest: a.Acceptance, Role: "CODER",
		ProhibitedActions: []string{}, RequiredOutputSchema: "taxion.dispatch-result/v1",
	})
	return err
}

func (r *Records) SavePacket(packet core.RoleHandoffPacket) error {
	data, err := packet.MarshalJSON()
	if err != nil {
		return err
	}
	for _, previous := range r.Packets {
		if previous.Hash == packet.Hash() {
			if previous.Canonical != string(data) {
				return fmt.Errorf("immutable packet artifact conflict")
			}
			return nil
		}
	}
	r.Packets = append(r.Packets, PacketArtifact{Hash: packet.Hash(), Canonical: string(data)})
	return nil
}

func (r Records) ReadPacket(hash string) (core.RoleHandoffPacket, error) {
	for _, artifact := range r.Packets {
		if artifact.Hash == hash {
			packet, err := core.ReadRoleHandoffPacket([]byte(artifact.Canonical), hash)
			if err != nil {
				return core.RoleHandoffPacket{}, err
			}
			data, err := packet.MarshalJSON()
			if err != nil || string(data) != artifact.Canonical {
				return core.RoleHandoffPacket{}, fmt.Errorf("packet artifact is not canonical")
			}
			return packet, nil
		}
	}
	return core.RoleHandoffPacket{}, fmt.Errorf("canonical packet artifact is missing")
}

func (r Records) PacketAuthorization(taskID string) (PacketAuthorization, bool) {
	for i := len(r.PacketAuthorizations) - 1; i >= 0; i-- {
		if r.PacketAuthorizations[i].TaskID == taskID {
			return r.PacketAuthorizations[i], true
		}
	}
	return PacketAuthorization{}, false
}

func (r Records) validatePackets() error {
	seen := map[string]bool{}
	for _, artifact := range r.Packets {
		if seen[artifact.Hash] {
			return fmt.Errorf("duplicate packet artifact")
		}
		seen[artifact.Hash] = true
		if _, err := r.ReadPacket(artifact.Hash); err != nil {
			return err
		}
	}
	authorizations := map[string]PacketAuthorization{}
	for _, authorization := range r.PacketAuthorizations {
		if err := authorization.Verify(); err != nil {
			return err
		}
		if _, exists := authorizations[authorization.Hash]; exists {
			return fmt.Errorf("duplicate packet authorization")
		}
		authorizations[authorization.Hash] = authorization
	}
	keys := map[string]bool{}
	for _, audit := range r.PacketAudits {
		auth, ok := authorizations[audit.AuthorizationHash]
		key := audit.DispatchID + ":" + audit.Role + ":" + audit.EvidenceHash
		if !ok || audit.Version != 1 || audit.TaskID != auth.TaskID || audit.DispatchID == "" ||
			!recordHash(audit.InstructionHash) || audit.At.IsZero() || keys[key] {
			return fmt.Errorf("invalid packet audit binding")
		}
		keys[key] = true
		packet, err := r.ReadPacket(audit.PacketHash)
		if err != nil {
			return err
		}
		fields := packet.Readback()
		if fields.TaskID != auth.TaskID || fields.TaskHash != auth.TaskHash ||
			fields.BaselineSHA != auth.BaselineSHA || fields.ScopeDigest != auth.ScopeDigest ||
			fields.AcceptanceDigest != auth.Acceptance || fields.Role != audit.Role {
			return fmt.Errorf("packet does not match its authorized contract")
		}
		if audit.Validation != "" {
			if audit.Role != "VALIDATOR" || core.Redact(audit.Validation) != audit.Validation ||
				core.SHA256Hex([]byte(audit.Validation)) != audit.EvidenceHash {
				return fmt.Errorf("invalid packet validation evidence")
			}
		} else if audit.EvidenceHash != "" {
			return fmt.Errorf("packet audit lacks validation evidence")
		}
	}
	return nil
}
