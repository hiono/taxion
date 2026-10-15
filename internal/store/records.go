package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Records shares the workspace transaction for recovery and approved publication.
type Records struct {
	Events   []RuntimeReceipt `json:"events,omitempty"`
	Updates  []ExternalUpdate `json:"updates,omitempty"`
	Findings []FailureRecord  `json:"findings,omitempty"`
	Requests []RequestRecord  `json:"requests,omitempty"`
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
	ID             string    `json:"id"`
	TaskID         string    `json:"taskId"`
	FlowID         string    `json:"flowId,omitempty"`
	Provider       string    `json:"provider"`
	Host           string    `json:"host"`
	Repository     string    `json:"repository"`
	TaskRepository string    `json:"taskRepository,omitempty"`
	Kind           string    `json:"kind"`
	Number         int       `json:"number"`
	Action         string    `json:"action"`
	Body           string    `json:"body"`
	Title          string    `json:"title,omitempty"`
	Hash           string    `json:"hash"`
	Actor          string    `json:"actor"`
	ApprovalSource string    `json:"approvalSource"`
	Account        string    `json:"account"`
	CredentialRef  string    `json:"credentialRef"`
	Status         string    `json:"status"`
	Claim          string    `json:"claim,omitempty"`
	RemoteID       string    `json:"remoteId,omitempty"`
	Error          string    `json:"error,omitempty"`
	At             time.Time `json:"at"`
}

func (r Records) validate() error {
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
