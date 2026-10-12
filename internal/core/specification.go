package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Specification is a quoted source snapshot, separate from the Task's authority.
type Specification struct {
	ID           string                 `json:"id"`
	Title        string                 `json:"title"`
	Source       string                 `json:"source"`
	Location     string                 `json:"location"`
	Version      string                 `json:"version"`
	RetrievedAt  time.Time              `json:"retrievedAt"`
	Body         string                 `json:"body"`
	LocalPath    string                 `json:"localPath,omitempty"`
	DocumentHash string                 `json:"documentHash,omitempty"`
	Usage        string                 `json:"usage"`
	Ingestion    *SpecificationApproval `json:"ingestion,omitempty"`
	Copilot      *SpecificationApproval `json:"copilot,omitempty"`
	Publication  *SpecificationApproval `json:"publication,omitempty"`
}

type SpecificationApproval struct {
	Actor  string `json:"actor"`
	Source string `json:"source"`
	Hash   string `json:"hash"`
}

// SpecificationHash binds each independently granted use to the exact source,
// excerpt, version and declared usage. Permission fields do not grant each other.
func SpecificationHash(spec Specification) (string, error) {
	spec.Ingestion, spec.Copilot, spec.Publication = nil, nil, nil
	if !nonempty(spec.ID) || !nonempty(spec.Title) || !nonempty(spec.Source) ||
		!nonempty(spec.Location) || !nonempty(spec.Version) || spec.RetrievedAt.IsZero() ||
		!nonempty(spec.Body) || len(spec.Body) > 1<<20 || !nonempty(spec.Usage) ||
		(spec.LocalPath == "") != (spec.DocumentHash == "") ||
		spec.LocalPath != "" && (!filepath.IsAbs(spec.LocalPath) || !validDocumentHash(spec.DocumentHash)) {
		return "", graphError("specification requires source, location, version, retrieval time, bounded excerpt and usage")
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

func validDocumentHash(hash string) bool {
	raw, err := hex.DecodeString(strings.TrimPrefix(hash, "sha256:"))
	return strings.HasPrefix(hash, "sha256:") && err == nil && len(raw) == sha256.Size &&
		hash == "sha256:"+hex.EncodeToString(raw)
}

func validateSpecifications(specs []Specification) error {
	ids := map[string]bool{}
	for _, spec := range specs {
		hash, err := SpecificationHash(spec)
		if err != nil {
			return err
		}
		if ids[spec.ID] {
			return graphError("duplicate specification ID %s", spec.ID)
		}
		ids[spec.ID] = true
		if spec.Ingestion == nil {
			return graphError("specification %s requires explicit ingestion approval", spec.ID)
		}
		for _, approval := range []*SpecificationApproval{spec.Ingestion, spec.Copilot, spec.Publication} {
			if approval != nil && (!nonempty(approval.Actor) || !nonempty(approval.Source) || approval.Hash != hash) {
				return graphError("specification %s approval differs from the exact snapshot", spec.ID)
			}
		}
	}
	return nil
}

// CheckSpecificationUse is the outgoing-data gate. Local source observations
// are repeated at use time; saved snapshots remain readable for recovery.
func CheckSpecificationUse(specs []Specification, purpose string) error {
	if purpose != "copilot" && purpose != "publication" {
		return graphError("unknown specification use %s", purpose)
	}
	if err := validateSpecifications(specs); err != nil {
		return err
	}
	for _, spec := range specs {
		approval := spec.Copilot
		if purpose == "publication" {
			approval = spec.Publication
		}
		if approval == nil {
			return graphError("specification %s lacks %s approval", spec.ID, purpose)
		}
		if spec.LocalPath != "" {
			info, err := os.Stat(spec.LocalPath)
			if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
				return graphError("specification %s requires an available regular source within 256 MiB", spec.ID)
			}
			file, err := os.Open(spec.LocalPath)
			if err != nil {
				return graphError("specification %s source unavailable: %v", spec.ID, err)
			}
			info, err = file.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
				file.Close()
				return graphError("specification %s requires a regular source document", spec.ID)
			}
			hash := sha256.New()
			readBytes, readErr := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
			closeErr := file.Close()
			if readErr != nil || closeErr != nil {
				return graphError("specification %s source observation failed: %v", spec.ID, errors.Join(readErr, closeErr))
			}
			if readBytes > 256<<20 {
				return graphError("specification %s source exceeds 256 MiB", spec.ID)
			}
			if fmt.Sprintf("sha256:%x", hash.Sum(nil)) != spec.DocumentHash {
				return graphError("specification %s source document changed; approve a new snapshot", spec.ID)
			}
		}
	}
	return nil
}

func cloneSpecifications(specs []Specification) []Specification {
	out := slices.Clone(specs)
	for i := range out {
		for _, approval := range []**SpecificationApproval{&out[i].Ingestion, &out[i].Copilot, &out[i].Publication} {
			if *approval != nil {
				copy := **approval
				*approval = &copy
			}
		}
	}
	return out
}

// CheckTaskSpecificationUse follows saved result decisions, so quoting an older
// Dispatch never launders its source permissions through a new Task.
func (g TaskGraph) CheckTaskSpecificationUse(taskID, purpose string) error {
	task, ok := g.GraphTask(taskID)
	if !ok {
		return graphError("specification use requires a tracked Task")
	}
	seen := map[string]bool{}
	checked := map[[sha256.Size]byte]bool{}
	checkSpecs := func(specs []Specification) error {
		effective := cloneSpecifications(specs)
		for i, historical := range effective {
			hash, err := SpecificationHash(historical)
			if err != nil {
				return err
			}
			for _, current := range task.Specifications {
				currentHash, err := SpecificationHash(current)
				if err != nil {
					return err
				}
				if hash == currentHash {
					effective[i] = current
					break
				}
			}
		}
		if err := validateSpecifications(effective); err != nil {
			return err
		}
		for _, spec := range effective {
			data, err := json.Marshal(spec)
			if err != nil {
				return err
			}
			key := sha256.Sum256(data)
			if checked[key] {
				continue
			}
			if err := CheckSpecificationUse([]Specification{spec}, purpose); err != nil {
				return err
			}
			checked[key] = true
		}
		return nil
	}
	var checkDecisions func([]TaskDecision) error
	checkDecisions = func(decisions []TaskDecision) error {
		for _, decision := range decisions {
			if !strings.HasPrefix(decision.ID, "result:") {
				continue
			}
			id := strings.TrimPrefix(decision.ID, "result:")
			if seen[id] {
				continue
			}
			seen[id] = true
			dispatch, ok := g.GraphDispatch(id)
			if !ok {
				return graphError("specification provenance Dispatch %s is missing", id)
			}
			if err := checkSpecs(dispatch.Instruction.Specifications); err != nil {
				return err
			}
			if err := checkDecisions(dispatch.Instruction.Decisions); err != nil {
				return err
			}
		}
		return nil
	}
	check := func(specs []Specification, decisions []TaskDecision) error {
		if err := checkSpecs(specs); err != nil {
			return err
		}
		return checkDecisions(decisions)
	}
	if err := check(task.Specifications, task.Decisions); err != nil {
		return err
	}
	for _, event := range g.History {
		if event.TaskID == taskID {
			if err := check(event.Task.Specifications, event.Task.Decisions); err != nil {
				return err
			}
		}
	}
	for _, dispatch := range g.Dispatches {
		if dispatch.TaskID == taskID {
			if err := check(dispatch.Instruction.Specifications, dispatch.Instruction.Decisions); err != nil {
				return err
			}
		}
	}
	for _, flow := range g.ReviewFlows {
		if flow.TaskID == taskID {
			if err := check(flow.Contract.Specifications, flow.Contract.Decisions); err != nil {
				return err
			}
		}
	}
	return nil
}
