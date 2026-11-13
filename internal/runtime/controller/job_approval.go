package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

// JobContractProposalRequest describes one explicit, versioned Job authority proposal.
type JobContractProposalRequest struct {
	JobID    string
	Contract core.JobContract
	Actor    string
	Source   string
	Reason   string
}

// JobApprovalConfirmationRequest binds a human decision to the exact proposal hash.
type JobApprovalConfirmationRequest struct {
	JobID       string
	ExpectedHash string
	Actor       string
	Reason      string
}

// ProposeJobContract stores a new Job contract revision and returns the exact
// proposal snapshot/hash that must be shown to the user before confirmation.
func ProposeJobContract(db *store.Store, request JobContractProposalRequest) (core.JobApprovalProposal, string, error) {
	if db == nil || strings.TrimSpace(request.JobID) == "" ||
		strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Source) == "" ||
		strings.TrimSpace(request.Reason) == "" {
		return core.JobApprovalProposal{}, "", fmt.Errorf("taxion: Job contract proposal requires Job ID, actor, source and reason")
	}
	var proposal core.JobApprovalProposal
	var proposalHash string
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		job, ok := state.Graph.Job(request.JobID)
		if !ok {
			return fmt.Errorf("taxion: Job contract proposal references an unknown Job")
		}
		if job.Status != core.JobDraft && job.Status != core.JobActive && job.Status != core.JobWaiting {
			return fmt.Errorf("taxion: Job contract cannot be revised in status %s", job.Status)
		}
		contract := request.Contract
		if job.Contract == nil {
			contract.Revision = 1
		} else {
			contract.Revision = job.Contract.Revision
			currentHash, err := core.CanonicalSHA256(*job.Contract)
			if err != nil {
				return err
			}
			candidateHash, err := core.CanonicalSHA256(contract)
			if err != nil {
				return err
			}
			if candidateHash == currentHash {
				proposal, err = core.JobProposal(job)
				if err != nil {
					return err
				}
				proposalHash, err = proposal.Hash()
				return err
			}
			contract.Revision = job.Contract.Revision + 1
		}
		if err := contract.Validate(); err != nil {
			return err
		}
		if job.Status == core.JobActive {
			if err := state.Graph.TransitionJob(job.ID, core.JobWaiting, request.Actor,
				"Job contract revision proposed: "+request.Reason, time.Now().UTC()); err != nil {
				return err
			}
		}
		for i := range state.Graph.Jobs {
			if state.Graph.Jobs[i].ID == job.ID {
				state.Graph.Jobs[i].Contract = &contract
				state.Graph.Jobs[i].UpdatedAt = time.Now().UTC()
				break
			}
		}
		updated, ok := state.Graph.Job(job.ID)
		if !ok {
			return fmt.Errorf("taxion: Job disappeared while proposing its contract")
		}
		proposal, err := core.JobProposal(updated)
		if err != nil {
			return err
		}
		proposalHash, err = proposal.Hash()
		return err
	})
	if err != nil {
		return core.JobApprovalProposal{}, "", err
	}
	return proposal, proposalHash, nil
}

// ConfirmJob records a user approval only if the currently stored proposal
// still matches the hash that was presented for confirmation.
func ConfirmJob(db *store.Store, request JobApprovalConfirmationRequest) (core.JobApproval, error) {
	if db == nil || strings.TrimSpace(request.JobID) == "" ||
		strings.TrimSpace(request.ExpectedHash) == "" || strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Reason) == "" {
		return core.JobApproval{}, fmt.Errorf("taxion: Job approval requires Job ID, expected hash, actor and reason")
	}
	approvalID, err := newIntakeJobID()
	if err != nil {
		return core.JobApproval{}, err
	}
	approvalID = "job-approval-" + strings.TrimPrefix(approvalID, "job-")
	var result core.JobApproval
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		job, ok := state.Graph.Job(request.JobID)
		if !ok {
			return fmt.Errorf("taxion: Job approval references an unknown Job")
		}
		proposal, err := core.JobProposal(job)
		if err != nil {
			return err
		}
		hash, err := proposal.Hash()
		if err != nil {
			return err
		}
		if hash != request.ExpectedHash {
			return fmt.Errorf("taxion: Job contract changed after approval proposal was presented")
		}
		if existing, ok := state.Graph.CurrentJobApproval(job.ID); ok {
			result = existing
			return nil
		}
		if job.Status != core.JobDraft && job.Status != core.JobWaiting && job.Status != core.JobActive {
			return fmt.Errorf("taxion: Job cannot be approved in status %s", job.Status)
		}
		at := time.Now().UTC()
		approval, err := core.NewJobApproval(approvalID, request.Actor, "user", request.Reason, proposal, at)
		if err != nil {
			return err
		}
		if err := state.Graph.AppendJobApproval(approval); err != nil {
			return err
		}
		if job.Status != core.JobActive {
			if err := state.Graph.TransitionJob(job.ID, core.JobActive, request.Actor,
				"Job contract explicitly approved", at); err != nil {
				return err
			}
		}
		result = approval
		return nil
	})
	if err != nil {
		return core.JobApproval{}, err
	}
	return result, nil
}
