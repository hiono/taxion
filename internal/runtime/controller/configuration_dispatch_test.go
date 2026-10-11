package controller

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestDispatchConfigurationUsesIssuedApprovalAndCurrentTask(t *testing.T) {
	for _, failure := range []string{"", "absent-snapshot", "candidate", "blocked", "cancelled", "cancel-requested", "suspended"} {
		t.Run(failure, func(t *testing.T) {
			candidate, err := config.Load([]byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`))
			if err != nil {
				t.Fatal(err)
			}
			approval := config.Approval{Kind: config.ApprovalKind, TaskID: "task", Reason: "authorize this combination", Allowed: []config.Config{candidate}}
			alternative := candidate
			alternative.Runtime.Account = "other-ai-user"
			approval.Allowed = append(approval.Allowed, alternative)
			data, err := json.Marshal(approval)
			if err != nil {
				t.Fatal(err)
			}
			decision := core.TaskDecision{ID: "approval", Source: "human:selection", Text: string(data)}
			task := core.GraphTask{
				ID: "task", Title: "approved execution", Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #69",
				Status: core.Draft, Owner: "CODER", Reviewer: "REVIEWER", CreatedFrom: "human:request",
				Objective: "approved work", Scope: []string{"src"}, Acceptance: []string{"tests pass"},
				ValidationCommands: []string{"go test ./..."}, RequiredEvidence: []string{"report"}, ExitCondition: "tests pass",
				Worktree: t.TempDir(), Branch: "work/task", BaselineSHA: strings.Repeat("a", 40),
				AuthorizationID: "authorization", Decisions: []core.TaskDecision{{ID: "authorization", Source: "human:1", Text: "authorize scope"}},
			}
			if failure != "absent-snapshot" {
				task.Decisions = append(task.Decisions, decision)
			}
			graph := core.TaskGraph{Version: 1}
			if err := graph.Track(task, "human", "register"); err != nil {
				t.Fatal(err)
			}
			if err := graph.Transition(task.ID, core.Ready, "human", "approve", core.TransitionProof{}); err != nil {
				t.Fatal(err)
			}
			dispatch, err := core.NewGraphDispatch(graph, "dispatch", task.ID, task.Repo, core.RelationshipNone, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := graph.IssueDispatch(dispatch, "controller", "issue"); err != nil {
				t.Fatal(err)
			}
			if failure == "absent-snapshot" {
				task, _ = graph.GraphTask(task.ID)
				task.Decisions = append(task.Decisions, decision)
				// Exercise a valid later Task record, without editing the issued instruction.
				graph.Tasks[0] = task
				graph.History = append(graph.History, core.GraphEvent{
					TaskID: task.ID, Actor: "human", Reason: "later approval record", At: graph.History[len(graph.History)-1].At, Task: task,
				})
			}
			if failure == "blocked" {
				if err := graph.Transition(task.ID, core.Blocked, "human", "pause", core.TransitionProof{}); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "cancel-requested" || failure == "suspended" {
				if err := graph.TransitionDispatch(dispatch.ID, core.DispatchAcknowledged, "worker", "received", true, false, nil); err != nil {
					t.Fatal(err)
				}
				if err := graph.TransitionDispatch(dispatch.ID, core.DispatchRunning, "worker", "started", true, true, nil); err != nil {
					t.Fatal(err)
				}
				status := core.DispatchCancelRequested
				var result *core.DispatchResult
				if failure == "suspended" {
					status = core.DispatchSuspended
					result = &core.DispatchResult{
						DispatchID: dispatch.ID, TaskID: task.ID, Recipient: dispatch.Recipient,
						Repository: task.Repo, Status: status, StartedSHA: dispatch.ExpectedStartSHA(),
						Evidence: []string{"human:pause"}, ResumeCondition: "new authorization", NextTransition: core.Blocked,
					}
				}
				if err := graph.TransitionDispatch(dispatch.ID, status, "human", "stop", false, false, result); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "cancelled" {
				result := &core.DispatchResult{DispatchID: dispatch.ID, TaskID: task.ID, Recipient: dispatch.Recipient,
					Repository: task.Repo, Status: core.DispatchCancelled, StartedSHA: dispatch.ExpectedStartSHA(),
					Evidence: []string{"human:cancel"}, NextTransition: core.Blocked}
				if err := graph.TransitionDispatch(dispatch.ID, core.DispatchCancelled, "human", "cancel", false, false, result); err != nil {
					t.Fatal(err)
				}
			}
			dbPath := filepath.Join(t.TempDir(), "workspace.db")
			db, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.InitializeWorkspace(store.Workspace{
				HasGraph: true, Graph: graph,
				Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}},
					Dispatches: []core.Dispatch{{ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: failure != "cancelled", CreatedAt: dispatch.CreatedAt}}},
			}); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if failure == "candidate" {
				candidate.Runtime.ContractRef = "different"
			}
			got, err := CheckDispatchConfiguration(db, dispatch.ID, candidate)
			if (err == nil) != (failure == "") || failure == "" && got != decision {
				t.Fatal("Dispatch approval boundary mismatch", failure, err)
			}
			if failure == "absent-snapshot" {
				if !strings.Contains(err.Error(), "absent from the saved Dispatch instruction") {
					t.Fatal("later approval rejected for an unrelated reason", err)
				}
				if _, err := CheckConfiguration(db, task.ID, candidate); err != nil {
					t.Fatal("fixture does not have a valid current Task approval", err)
				}
			}
			if _, err := CheckDispatchConfiguration(db, "missing", candidate); err == nil {
				t.Fatal("missing Dispatch accepted")
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("read-only check changed durable workspace", err)
			}
			input, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := config.Resolve([]config.Layer{{Source: "user", Input: input}})
			if err != nil {
				t.Fatal(err)
			}
			if failure != "" {
				if err := BindConfiguration(db, dispatch.ID, resolved); err == nil {
					t.Fatal("rejected configuration was persisted")
				}
				after, err := db.LoadWorkspace()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("rejected binding changed workspace", err)
				}
				return
			}
			delete(resolved.Sources, "runtime.account")
			if err := BindConfiguration(db, dispatch.ID, resolved); err == nil {
				t.Fatal("missing field origin accepted")
			}
			resolved.Sources["runtime.account"] = "user"
			legacy := before.Graph
			if err := legacy.BindRuntimeSession(dispatch.ID, "legacy-session", "legacy-hash"); err != nil {
				t.Fatal(err)
			}
			if err := legacy.BindDispatchConfiguration(dispatch.ID, core.DispatchConfiguration{
				DecisionID: decision.ID, Selection: input, Sources: resolved.Sources, RecordedAt: time.Now().UTC(),
			}); err == nil {
				t.Fatal("settings retrofitted to a legacy session")
			}
			ownedGraph := before.Graph
			binding := core.DispatchConfiguration{
				DecisionID: decision.ID, Selection: append(json.RawMessage(nil), input...),
				Sources: map[string]string{}, RecordedAt: time.Now().UTC(),
			}
			for field, source := range resolved.Sources {
				binding.Sources[field] = source
			}
			if err := ownedGraph.BindDispatchConfiguration(dispatch.ID, binding); err != nil {
				t.Fatal(err)
			}
			binding.Selection[0] = '!'
			binding.Sources["runtime.account"] = "invalid"
			if err := ownedGraph.Validate(); err != nil {
				t.Fatal("caller mutated owned binding", err)
			}
			original, _ := before.Graph.GraphDispatch(dispatch.ID)
			if original.Configuration != nil {
				t.Fatal("binding modified the previous graph")
			}
			corrupt := ownedGraph
			boundCopy, _ := corrupt.GraphDispatch(dispatch.ID)
			boundCopy.Configuration.Sources["runtime.account"] = "invalid"
			if err := corrupt.Validate(); err == nil {
				t.Fatal("invalid stored field origins accepted")
			}
			if err := BindConfiguration(db, dispatch.ID, resolved); err != nil {
				t.Fatal(err)
			}
			boundState, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			bound, _ := boundState.Graph.GraphDispatch(dispatch.ID)
			if bound.Configuration == nil || bound.Configuration.DecisionID != decision.ID ||
				!reflect.DeepEqual(bound.Configuration.Selection, json.RawMessage(input)) ||
				!reflect.DeepEqual(bound.Configuration.Sources, resolved.Sources) {
				t.Fatal("complete selection or origins not persisted")
			}
			resolved.Sources["runtime.account"] = "environment"
			if err := BindConfiguration(db, dispatch.ID, resolved); err != nil {
				t.Fatal("same selection cannot be checked idempotently", err)
			}
			repeated, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(boundState, repeated) {
				t.Fatal("rebinding rewrote original origins or recording time", err)
			}
			if _, err := CheckConfiguration(db, task.ID, alternative); err != nil {
				t.Fatal("alternative is not approved by the fixture", err)
			}
			if _, err := CheckDispatchConfiguration(db, dispatch.ID, alternative); err == nil {
				t.Fatal("approved alternative replaced the selected tuple")
			}
			resolved.Config = alternative
			if err := BindConfiguration(db, dispatch.ID, resolved); err == nil {
				t.Fatal("selected configuration changed")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			reloaded, err := reopened.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(boundState, reloaded) {
				t.Fatal("saved selection did not survive SQLite reopen", err)
			}
			if _, err := CheckDispatchConfiguration(reopened, dispatch.ID, candidate); err != nil {
				t.Fatal("saved selection rejected after reopen", err)
			}
		})
	}
}
