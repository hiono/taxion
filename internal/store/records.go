package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/publictext"
)

// Records shares the workspace transaction for recovery and approved publication.
type Records struct {
	Operation               *Operation                      `json:"operation,omitempty"`
	Packets                 []PacketArtifact                `json:"packets,omitempty"`
	PacketAuthorizations    []PacketAuthorization           `json:"packetAuthorizations,omitempty"`
	PacketAudits            []PacketAudit                   `json:"packetAudits,omitempty"`
	Events                  []RuntimeReceipt                `json:"events,omitempty"`
	Budgets                 []BudgetAudit                   `json:"budgets,omitempty"`
	Updates                 []ExternalUpdate                `json:"updates,omitempty"`
	Cancellations           []PublicationCancellation       `json:"publicationCancellations,omitempty"`
	Findings                []FailureRecord                 `json:"findings,omitempty"`
	Requests                []RequestRecord                 `json:"requests,omitempty"`
	Restore                 *RestoreRecord                  `json:"restore,omitempty"`
	Restores                []RestoreRecord                 `json:"restores,omitempty"`
	ReviewAcceptances       []ReviewAcceptance              `json:"reviewAcceptances,omitempty"`
	DispatchReconciliations []StoppedDispatchReconciliation `json:"dispatchReconciliations,omitempty"`
}

type ReviewAcceptance struct {
	FlowID                    string          `json:"flowId"`
	DispatchID                string          `json:"dispatchId"`
	InstructionHash           string          `json:"instructionHash"`
	ResultHash                string          `json:"resultHash"`
	PreviewHash               string          `json:"previewHash"`
	Actor                     string          `json:"actor"`
	Source                    string          `json:"source"`
	Reason                    string          `json:"reason"`
	AllowExpired              bool            `json:"allowExpired"`
	OriginalReasonUnavailable bool            `json:"originalReasonUnavailable"`
	PreviousFlow              core.ReviewFlow `json:"previousFlow"`
	At                        time.Time       `json:"at"`
}

func (r Records) VerifyReviewAcceptances(graph core.TaskGraph) error {
	seen := map[string]bool{}
	for _, audit := range r.ReviewAcceptances {
		prior := audit.PreviousFlow
		current, exists := graph.ReviewFlow(audit.FlowID)
		review, found := graph.GraphDispatch(audit.DispatchID)
		resultHash, err := core.CanonicalSHA256(review.Result)
		task, taskFound := graph.GraphTask(prior.TaskID)
		if seen[audit.FlowID] || !exists || !found || !taskFound || audit.FlowID != prior.ID ||
			audit.Actor == "" || audit.Source == "" || audit.Reason == "" || audit.At.IsZero() ||
			!audit.OriginalReasonUnavailable || !recordHash(audit.PreviewHash) ||
			err != nil || resultHash != audit.ResultHash || review.InstructionHash != audit.InstructionHash ||
			review.Status != core.DispatchSucceeded || review.Result == nil || review.Result.ReviewVerdict != "APPROVE" ||
			prior.Status != "STOPPED" || prior.StopCode != "SAFETY_STOP" || prior.Reason == "" ||
			!prior.HandoffHeld() || prior.RunnerID != "" || len(prior.DispatchIDs) != 1 ||
			prior.DispatchIDs[0] != review.ID || prior.SourceDispatchID == "" ||
			task.Status != core.Completed || task.TargetSHA != review.Result.OutputSHA ||
			task.TargetTreeSHA != review.Result.OutputTreeSHA ||
			!audit.At.Before(prior.Deadline) && !audit.AllowExpired {
			return fmt.Errorf("invalid saved review acceptance audit")
		}
		prior.Status, prior.StopCode, prior.StopCause = "COMPLETED", "", ""
		prior.Reason = "saved independent review and acceptance verified for " + review.Result.OutputTreeSHA
		if !reflect.DeepEqual(prior, current) {
			return fmt.Errorf("accepted Flow differs from its retained stop audit")
		}
		seen[audit.FlowID] = true
	}
	return nil
}

type PublicationCancellation struct {
	ID             string    `json:"id"`
	PreviousState  string    `json:"previousState"`
	ApprovalSource string    `json:"approvalSource"`
	ContentHash    string    `json:"contentHash"`
	Reason         string    `json:"reason"`
	Actor          string    `json:"actor"`
	Authorization  string    `json:"authorization"`
	At             time.Time `json:"at"`
}

type BudgetAudit struct {
	FlowID   string    `json:"flowId"`
	TaskID   string    `json:"taskId"`
	Sequence uint64    `json:"sequence"`
	Kind     string    `json:"kind"`
	Payload  string    `json:"payload"`
	Hash     string    `json:"hash"`
	At       time.Time `json:"at"`
}

type RestoredPath struct {
	Original string `json:"original"`
	Local    string `json:"local"`
}

type RestoreRecord struct {
	SnapshotHash string         `json:"snapshotHash"`
	Actor        string         `json:"actor"`
	Source       string         `json:"source"`
	TaskIDs      []string       `json:"taskIds"`
	Paths        []RestoredPath `json:"paths"`
	At           time.Time      `json:"at"`
}

// RestoredDispatch locates Git evidence without changing the saved contract.
func (r Records) RestoredDispatch(d core.GraphDispatch) core.GraphDispatch {
	if r.Restore == nil {
		return d
	}
	resolve := func(path string) string {
		for _, mapping := range r.Restore.Paths {
			if mapping.Original == path {
				return mapping.Local
			}
		}
		return path
	}
	d.Instruction.Worktree = resolve(d.Instruction.Worktree)
	d.ReviewWorktreeSource = resolve(d.ReviewWorktreeSource)
	if d.Result != nil {
		result := *d.Result
		d.Result = &result
		result.Validations = slices.Clone(result.Validations)
		for i := range result.Validations {
			result.Validations[i].Worktree = resolve(result.Validations[i].Worktree)
		}
		if result.GitEvidence != nil {
			evidence := *result.GitEvidence
			evidence.Worktree = resolve(evidence.Worktree)
			result.GitEvidence = &evidence
		}
		if result.PartialGitEvidence != nil {
			evidence := *result.PartialGitEvidence
			evidence.Worktree = resolve(evidence.Worktree)
			result.PartialGitEvidence = &evidence
		}
	}
	return d
}

type RequestRecord struct {
	RequestID string    `json:"requestId"`
	Kind      string    `json:"kind"`
	Sequence  uint64    `json:"sequence"`
	Payload   string    `json:"payload"`
	Hash      string    `json:"hash"`
	At        time.Time `json:"at"`
}

type FailureRecord struct {
	ID                 string    `json:"id"`
	TaskID             string    `json:"taskId"`
	DispatchID         string    `json:"dispatchId"`
	ResultHash         string    `json:"resultHash"`
	Cause              string    `json:"cause"`
	Correction         string    `json:"correction"`
	LessonCandidate    string    `json:"lessonCandidate"`
	ValidationEvidence []string  `json:"validationEvidence"`
	Actor              string    `json:"actor"`
	DecisionSource     string    `json:"decisionSource"`
	At                 time.Time `json:"at"`
}

type RuntimeReceipt struct {
	DispatchID string    `json:"dispatchId"`
	SessionID  string    `json:"sessionId"`
	StreamID   string    `json:"streamId"`
	Sequence   uint64    `json:"sequence"`
	Type       string    `json:"type"`
	Payload    string    `json:"payload"`
	Hash       string    `json:"hash"`
	At         time.Time `json:"at"`
}

type ExternalUpdate struct {
	GuidanceDigest string                      `json:"guidanceDigest"`
	SemanticReview *publictext.SemanticReview  `json:"semanticReview,omitempty"`
	Statement      publictext.StatementContext `json:"statement,omitempty"`
	ID             string                      `json:"id"`
	TaskID         string                      `json:"taskId"`
	FlowID         string                      `json:"flowId,omitempty"`
	Provider       string                      `json:"provider"`
	Host           string                      `json:"host"`
	Repository     string                      `json:"repository"`
	TaskRepository string                      `json:"taskRepository,omitempty"`
	Kind           string                      `json:"kind"`
	Number         int                         `json:"number"`
	Action         string                      `json:"action"`
	Body           string                      `json:"body"`
	Title          string                      `json:"title,omitempty"`
	Hash           string                      `json:"hash"`
	Actor          string                      `json:"actor"`
	ApprovalSource string                      `json:"approvalSource"`
	Account        string                      `json:"account"`
	CredentialRef  string                      `json:"credentialRef"`
	Status         string                      `json:"status"`
	Claim          string                      `json:"claim,omitempty"`
	RemoteID       string                      `json:"remoteId,omitempty"`
	Error          string                      `json:"error,omitempty"`
	At             time.Time                   `json:"at"`
}

func (update ExternalUpdate) PublicText() publictext.Mutation {
	surface := "comment"
	if update.Action == "CREATE" || update.Action == "CLOSE" {
		surface = "issue"
	}
	return publictext.Mutation{Provider: update.Provider, Host: update.Host, Repository: update.Repository,
		Surface: surface, Title: update.Title, Body: update.Body, Statement: update.Statement,
		GuidanceDigest: update.GuidanceDigest, Review: update.SemanticReview}
}

func (r Records) validate() error {
	if r.Operation != nil {
		if err := r.Operation.validate(); err != nil {
			return err
		}
	}
	if err := r.validatePackets(); err != nil {
		return err
	}
	restores := r.Restores
	if r.Restore != nil {
		restores = append(slices.Clone(restores), *r.Restore)
	}
	for _, restore := range restores {
		if !recordHash(restore.SnapshotHash) || restore.Actor == "" || restore.Source == "" || restore.At.IsZero() {
			return fmt.Errorf("invalid workspace restore receipt")
		}
		seen := map[string]bool{}
		for _, mapping := range restore.Paths {
			if !filepath.IsAbs(mapping.Original) || !filepath.IsAbs(mapping.Local) || seen[mapping.Original] {
				return fmt.Errorf("invalid or duplicate restored path")
			}
			seen[mapping.Original] = true
		}
	}
	requestSequences := map[string]uint64{}
	for _, record := range r.Requests {
		key := record.RequestID + ":" + record.Kind
		requestSequences[key]++
		payload := []byte(record.Payload)
		if strings.TrimSpace(record.RequestID) == "" || record.Kind != "planning" && record.Kind != "execution" ||
			record.Sequence != requestSequences[key] || record.At.IsZero() || !json.Valid(payload) ||
			record.Hash != fmt.Sprintf("%x", sha256.Sum256(payload)) {
			return fmt.Errorf("invalid request record or incomplete sequence")
		}
	}
	seen := map[string]bool{}
	for _, update := range r.Updates {
		if update.ID == "" || seen[update.ID] || update.TaskID == "" || !recordHash(update.Hash) ||
			update.Actor == "" || update.ApprovalSource == "" || update.At.IsZero() {
			return fmt.Errorf("invalid or duplicate approved external update")
		}
		switch update.Status {
		case "PENDING":
			if update.Claim != "" || update.RemoteID != "" {
				return fmt.Errorf("pending external update has delivery evidence")
			}
		case "CANCELED":
			if update.Claim != "" || update.RemoteID != "" {
				return fmt.Errorf("canceled external update has delivery evidence")
			}
		case "CLAIMED", "UNKNOWN":
			if update.Claim == "" || update.RemoteID != "" {
				return fmt.Errorf("uncertain external update requires a sender claim")
			}
		case "SENT":
			if update.Claim == "" || update.RemoteID == "" {
				return fmt.Errorf("sent external update requires delivery evidence")
			}
		default:
			return fmt.Errorf("unknown external update state")
		}
		seen[update.ID] = true
	}
	canceled := map[string]bool{}
	for _, audit := range r.Cancellations {
		if canceled[audit.ID] || audit.PreviousState != "PENDING" || !recordHash(audit.ContentHash) ||
			strings.TrimSpace(audit.Reason) == "" || strings.TrimSpace(audit.Actor) == "" ||
			strings.TrimSpace(audit.Authorization) == "" || audit.At.IsZero() {
			return fmt.Errorf("invalid or duplicate publication cancellation audit")
		}
		matched := false
		for _, update := range r.Updates {
			if update.ID == audit.ID && update.Hash == audit.ContentHash && update.Status == "CANCELED" &&
				update.ApprovalSource == audit.ApprovalSource {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("cancellation audit differs from approved intent")
		}
		canceled[audit.ID] = true
	}
	for _, update := range r.Updates {
		if update.Status == "CANCELED" && !canceled[update.ID] {
			return fmt.Errorf("canceled publication requires append-only authorization")
		}
	}
	seen = map[string]bool{}
	budgetSequences := map[string]uint64{}
	for _, record := range r.Budgets {
		budgetSequences[record.FlowID]++
		if record.FlowID == "" || record.TaskID == "" ||
			record.Sequence != budgetSequences[record.FlowID] || record.At.IsZero() ||
			!slices.Contains([]string{"DISPATCH_ISSUED", "SESSION_STARTED", "USAGE_OBSERVED", "BUDGET_STOP", "RECONTRACT", "STAGE_OBSERVED",
				"INPUT_ACCEPTED", "INPUT_VALIDATION_STARTED", "INPUT_VALIDATION_FINISHED", "INPUT_VALIDATION_REUSED"}, record.Kind) ||
			!json.Valid([]byte(record.Payload)) ||
			record.Hash != fmt.Sprintf("%x", sha256.Sum256([]byte(record.Payload))) {
			return fmt.Errorf("invalid task budget audit record or sequence")
		}
	}
	seen = map[string]bool{}
	for _, event := range r.Events {
		key := fmt.Sprintf("%s:%s:%d", event.DispatchID, event.StreamID, event.Sequence)
		if event.DispatchID == "" || event.SessionID == "" || event.Type == "" || !recordHash(event.Hash) ||
			event.At.IsZero() || event.Type != "verified-result" && event.StreamID == "" ||
			(event.Sequence != 0 || event.Type == "verified-result") && seen[key] {
			return fmt.Errorf("invalid or duplicate runtime receipt")
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, finding := range r.Findings {
		if finding.ID == "" || seen[finding.ID] || finding.TaskID == "" || finding.DispatchID == "" ||
			!recordHash(finding.ResultHash) || finding.Cause == "" || finding.Correction == "" ||
			finding.LessonCandidate == "" || len(finding.ValidationEvidence) == 0 ||
			finding.Actor == "" || finding.DecisionSource == "" || finding.At.IsZero() {
			return fmt.Errorf("invalid or duplicate failure analysis")
		}
		seen[finding.ID] = true
	}
	return nil
}

func recordHash(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && len(value) == 64
}

// VerifyFlowAudits binds new stage plans on normalized and legacy loads as well
// as writes. Removing a plan must not silently restore legacy unlimited stages.
func (r Records) VerifyFlowAudits(graph core.TaskGraph) error {
	if err := r.VerifyDispatchReconciliations(graph); err != nil {
		return err
	}
	if err := r.VerifyReviewAcceptances(graph); err != nil {
		return err
	}
	bound := map[string]bool{}
	artifacts := map[string]*core.ArtifactReview{}
	for _, audit := range r.Budgets {
		input := slices.Contains([]string{"INPUT_ACCEPTED", "INPUT_VALIDATION_STARTED", "INPUT_VALIDATION_FINISHED", "INPUT_VALIDATION_REUSED"}, audit.Kind)
		if audit.Kind != "DISPATCH_ISSUED" && !input {
			continue
		}
		var report struct {
			Artifact    *core.ArtifactReview `json:"artifact"`
			ResumeBasis *struct {
				StagePlan         *core.StagePlan `json:"stagePlan"`
				ArtifactHash      string          `json:"artifactHash"`
				ArtifactBasisHash string          `json:"artifactBasisHash"`
			} `json:"resumeBasis"`
		}
		if err := json.Unmarshal([]byte(audit.Payload), &report); err != nil {
			return err
		}
		if report.ResumeBasis == nil {
			continue
		}
		flow, exists := graph.ReviewFlow(audit.FlowID)
		if !exists && report.ResumeBasis.StagePlan != nil ||
			exists && !reflect.DeepEqual(flow.StagePlan, report.ResumeBasis.StagePlan) {
			return fmt.Errorf("stage plan differs from immutable Flow issuance")
		}
		if input {
			if !exists || flow.Artifact == nil || report.Artifact == nil || audit.TaskID != flow.TaskID {
				return fmt.Errorf("input audit requires its saved artifact Flow")
			}
			basis, err := flow.Artifact.BasisHash()
			auditBasis, auditErr := report.Artifact.BasisHash()
			if err != nil || auditErr != nil || basis != auditBasis ||
				!flow.Artifact.ObservedAt.Equal(report.Artifact.ObservedAt) ||
				report.ResumeBasis.ArtifactBasisHash != basis ||
				report.ResumeBasis.ArtifactHash != flow.Artifact.Target.Hash {
				return fmt.Errorf("input artifact differs from immutable acceptance")
			}
			prior := artifacts[flow.ID]
			status := report.Artifact.Validation.Status
			switch audit.Kind {
			case "INPUT_VALIDATION_REUSED":
				source := report.Artifact.ValidationSourceFlowID
				original := artifacts[source]
				if prior != nil || original == nil || status != "PASSED" {
					return fmt.Errorf("validation reuse requires an existing passed append-only receipt")
				}
				expected := *original
				expected.ValidationSourceFlowID = source
				if !reflect.DeepEqual(expected, *report.Artifact) {
					return fmt.Errorf("validation reuse changed its source receipt")
				}
				reconciled := false
				for _, saved := range r.DispatchReconciliations {
					if saved.PreviousFlow.ID == source && saved.PreviousFlow.StoppedArtifactReviewer(saved.PreviousDispatch) {
						for _, decision := range flow.Contract.Decisions {
							reconciled = reconciled || decision.ID == "reconciled-input:"+flow.TaskID && decision.Text == saved.PreviewHash
						}
					}
				}
				if !reconciled {
					return fmt.Errorf("validation reuse lacks its proposal-bound Reviewer reconciliation")
				}
			case "INPUT_ACCEPTED":
				if prior != nil || status != "PENDING" || report.Artifact.ValidationSourceFlowID != "" {
					return fmt.Errorf("input acceptance must precede validation exactly once")
				}
			case "INPUT_VALIDATION_STARTED":
				if prior == nil || prior.Validation.Status != "PENDING" || status != "RUNNING" {
					return fmt.Errorf("input execution requires a unique durable start marker")
				}
			case "INPUT_VALIDATION_FINISHED":
				if prior == nil || prior.Validation.Status != "RUNNING" || (status != "PASSED" && status != "FAILED") ||
					!prior.Validation.StartedAt.Equal(report.Artifact.Validation.StartedAt) {
					return fmt.Errorf("input result requires its exact execution marker")
				}
			}
			artifacts[flow.ID] = report.Artifact
		}
		bound[audit.FlowID] = true
	}
	for _, flow := range graph.ReviewFlows {
		if flow.StagePlan != nil && !bound[flow.ID] {
			return fmt.Errorf("stage plan requires immutable issuance")
		}
		if flow.Artifact != nil && (artifacts[flow.ID] == nil ||
			!reflect.DeepEqual(flow.Artifact.Validation, artifacts[flow.ID].Validation)) {
			return fmt.Errorf("input validation requires its exact append-only execution receipt")
		}
	}
	return nil
}

func decodeRecords(data string) (Records, error) {
	var records Records
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return records, fmt.Errorf("decode workspace records: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return records, fmt.Errorf("workspace records contain trailing data")
	}
	if strings.TrimSpace(data) == "null" {
		return records, fmt.Errorf("workspace records require an object")
	}
	return records, records.validate()
}
