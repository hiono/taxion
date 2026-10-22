package main

import (
	"crypto/subtle"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func readyAuthorizationError(code string, err error) error {
	if outputErr := printJSON(struct {
		Code         string `json:"code"`
		Message      string `json:"message"`
		StateChanged bool   `json:"stateChanged"`
	}{code, core.Redact(err.Error()), false}); outputErr != nil {
		return outputErr
	}
	return authorizeError(err)
}

func checkReadyAuthorization(state store.Workspace, request graphRequest, configuration core.ApprovalConfiguration) (core.ApprovalBundle, error) {
	if !state.HasGraph || state.Graph.Version != 1 || state.Graph.ApprovalSchemaVersion < 0 ||
		state.Graph.ApprovalSchemaVersion > core.CurrentApprovalSchemaVersion {
		return core.ApprovalBundle{}, fmt.Errorf("unsupported Task Graph schema")
	}
	if err := state.Graph.Validate(); err != nil {
		return core.ApprovalBundle{}, err
	}
	if err := state.Graph.CheckLedger(state.Ledger); err != nil {
		return core.ApprovalBundle{}, err
	}
	task, ok := state.Graph.GraphTask(request.Task.ID)
	if !ok || task.Status != core.Ready || request.Task.Status != core.Ready {
		return core.ApprovalBundle{}, fmt.Errorf("fresh bundle authorization requires a saved READY Task")
	}
	recordHash, err := core.CanonicalSHA256(task)
	if err != nil {
		return core.ApprovalBundle{}, err
	}
	manifestHash, err := core.CanonicalSHA256(request.Task)
	if err != nil || manifestHash != recordHash || request.Concern != nil ||
		strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Reason) == "" {
		return core.ApprovalBundle{}, fmt.Errorf("manifest must exactly match the saved contract and include actor and reason without a new concern")
	}
	requestJSON, err := core.CanonicalJSON(request)
	if err != nil || core.Redact(string(requestJSON)) != string(requestJSON) {
		return core.ApprovalBundle{}, fmt.Errorf("authorization manifest contains a credential-like value")
	}
	for _, flow := range state.Graph.ReviewFlows {
		if flow.TaskID == task.ID {
			return core.ApprovalBundle{}, fmt.Errorf("Task already has a Flow")
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		if dispatch.TaskID == task.ID {
			return core.ApprovalBundle{}, fmt.Errorf("Task already has a Dispatch")
		}
	}
	for _, dispatch := range state.Ledger.Dispatches {
		if dispatch.TaskID == task.ID {
			return core.ApprovalBundle{}, fmt.Errorf("Task already has a ledger Dispatch")
		}
	}
	proposalHash, err := core.ImmutableProposalHash(task)
	if err != nil {
		return core.ApprovalBundle{}, err
	}
	selected, err := configurationValue(configuration.Selection)
	if err != nil || task.Repo != selected.Repository.Path ||
		task.HumanDecision != nil && task.HumanDecision.SelectedOptionID == "" {
		return core.ApprovalBundle{}, fmt.Errorf("configuration repository or pending human decision does not match the Task")
	}
	if task.AuthorizationID != "intake-confirmation:"+task.ID || !taskHasConfigurationApproval(task, configuration) {
		return core.ApprovalBundle{}, fmt.Errorf("Task authorization or existing configuration approval does not match")
	}
	for _, bundle := range state.Graph.ApprovalsForTask(task.ID) {
		if bundle.ProposalHash != proposalHash || bundle.RecordHash != recordHash ||
			bundle.Actor != request.Actor || bundle.Source != "human:"+request.Actor || bundle.Reason != request.Reason {
			return core.ApprovalBundle{}, fmt.Errorf("existing bundle conflicts with the requested authorization")
		}
		if err := bundle.Verify(task, configuration); err != nil {
			return core.ApprovalBundle{}, fmt.Errorf("existing bundle does not match: %w", err)
		}
	}
	if bundle, ok := state.Graph.MatchingApproval(task.ID, task, configuration); ok {
		return bundle, nil
	}
	return core.ApprovalBundle{}, nil
}

func taskAuthorizeReady(request graphRequest, configuration core.ApprovalConfiguration, confirm string) error {
	state, err := packetWorkspaceSnapshot()
	if err != nil {
		return readyAuthorizationError("ready-authorization-invalid", err)
	}
	existing, err := checkReadyAuthorization(state, request, configuration)
	if err != nil {
		return readyAuthorizationError("ready-authorization-invalid", err)
	}
	if existing.ID != "" {
		return printJSON(approvalReuseReport(existing))
	}
	snapshotHash, err := core.CanonicalSHA256(state)
	if err != nil {
		return readyAuthorizationError("ready-authorization-invalid", err)
	}
	token, err := core.CanonicalSHA256(struct {
		Version       int                        `json:"version"`
		WorkspaceHash string                     `json:"workspaceHash"`
		Request       graphRequest               `json:"request"`
		Configuration core.ApprovalConfiguration `json:"configuration"`
	}{1, snapshotHash, request, configuration})
	if err != nil {
		return readyAuthorizationError("ready-authorization-invalid", err)
	}
	proposalHash, err := core.ImmutableProposalHash(request.Task)
	if err != nil {
		return readyAuthorizationError("ready-authorization-invalid", err)
	}
	if confirm == "" {
		return printJSON(struct {
			Status            string `json:"status"`
			TaskID            string `json:"taskId"`
			ProposalHash      string `json:"proposalHash"`
			ConfirmationToken string `json:"confirmationToken"`
			StateChanged      bool   `json:"stateChanged"`
		}{"awaiting-approval", request.Task.ID, proposalHash, token, false})
	}
	if subtle.ConstantTimeCompare([]byte(confirm), []byte(token)) != 1 {
		return readyAuthorizationError("ready-authorization-stale", fmt.Errorf("confirmation does not match the complete source and configuration basis"))
	}
	path, err := filepath.Abs(workspaceDBPath())
	if err != nil {
		return readyAuthorizationError("ready-authorization-stale", err)
	}
	// Reserve the writer before reading the source basis: two deferred readers
	// can otherwise both lose their lock upgrade. The wait does not renew approval.
	location := url.URL{Scheme: "file", Path: path,
		RawQuery: "mode=rw&_txlock=immediate&_pragma=busy_timeout(5000)"}
	db, err := store.Open(location.String())
	if err != nil {
		return readyAuthorizationError("ready-authorization-stale", err)
	}
	defer db.Close()
	var saved core.ApprovalBundle
	err = db.UpdateWorkspace(func(current *store.Workspace) error {
		currentHash, err := core.CanonicalSHA256(*current)
		if err != nil {
			return err
		}
		freshConfiguration, err := approvalConfiguration()
		if err != nil {
			return err
		}
		if currentHash != snapshotHash || !reflect.DeepEqual(configuration, freshConfiguration) {
			return fmt.Errorf("source records or effective configuration changed during authorization")
		}
		if _, err := checkReadyAuthorization(*current, request, freshConfiguration); err != nil {
			return err
		}
		saved, err = core.ComposeReauthorizationApprovalBundleWithProposalHash(
			"fresh-ready-"+token[:24], proposalHash, request.Task, freshConfiguration,
			request.Actor, "human:"+request.Actor, request.Reason, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := saved.Verify(request.Task, freshConfiguration); err != nil {
			return err
		}
		return current.Graph.AppendApproval(saved)
	})
	if err != nil {
		return readyAuthorizationError("ready-authorization-stale", err)
	}
	readback, err := packetWorkspaceSnapshot()
	if err != nil {
		return authorizeError(err)
	}
	bundle, ok := readback.Graph.Approval(saved.ID)
	if !ok || !reflect.DeepEqual(bundle, saved) {
		return authorizeError(fmt.Errorf("saved fresh READY bundle did not match readback"))
	}
	if err := bundle.Verify(request.Task, configuration); err != nil {
		return authorizeError(err)
	}
	return printJSON(approvalReuseReport(bundle))
}
