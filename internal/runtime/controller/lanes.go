package controller

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// LaneName identifies one read-only controller lane. Lanes only observe saved
// inputs; they never write the Task Graph, the ledger, a worktree, a Dispatch
// or a runtime session.
type LaneName string

const (
	LaneRepositoryIdentity  LaneName = "repository-identity"
	LaneBaselineWorktree    LaneName = "baseline-worktree"
	LaneContractConsistency LaneName = "contract-consistency"
	LaneHandoffPreparation  LaneName = "handoff-preparation"
)

// ReadOnlyLaneNames is the deterministic aggregation order. The order never
// depends on completion timing.
var ReadOnlyLaneNames = []LaneName{
	LaneRepositoryIdentity,
	LaneBaselineWorktree,
	LaneContractConsistency,
	LaneHandoffPreparation,
}

const (
	LaneReportVersion  = 1
	LaneVerdictPass    = "pass"
	LaneVerdictFail    = "fail"
	DefaultLaneTimeout = 5 * time.Second
	MaxLaneConcurrency = 4
)

// LaneRequest carries everything a read-only lane may observe. Graph is the
// saved Task Graph and is never modified.
type LaneRequest struct {
	Graph          *core.TaskGraph
	TaskID         string
	RepositoryRoot string
	Timeout        time.Duration
}

// LaneResult is the machine-readable outcome of one lane.
type LaneResult struct {
	Lane        LaneName `json:"lane"`
	Verdict     string   `json:"verdict"`
	Summary     string   `json:"summary"`
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// LaneReport aggregates all lanes in ReadOnlyLaneNames order. ExecutionGateOpen
// is true only when every lane passed; a single failure closes the gate.
type LaneReport struct {
	Version           int          `json:"version"`
	TaskID            string       `json:"taskId"`
	Verdict           string       `json:"verdict"`
	ExecutionGateOpen bool         `json:"executionGateOpen"`
	Lanes             []LaneResult `json:"lanes"`
}

// laneStartHook lets tests prove parallel execution. It runs after a lane has
// acquired a concurrency slot and before the lane observes the graph.
var laneStartHook func(context.Context, LaneName)

// RunReadOnlyLanes executes the four read-only lanes with bounded parallelism
// and a per-lane timeout, then aggregates them deterministically. It starts no
// model session and performs no mutation.
func RunReadOnlyLanes(ctx context.Context, request LaneRequest) (LaneReport, error) {
	if request.Graph == nil {
		return LaneReport{}, errors.New("taxion: read-only lanes require a Task Graph")
	}
	if _, ok := request.Graph.GraphTask(request.TaskID); !ok {
		return LaneReport{}, fmt.Errorf("taxion: read-only lanes require an existing Task %q", request.TaskID)
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = DefaultLaneTimeout
	}
	hook := laneStartHook
	results := make([]LaneResult, len(ReadOnlyLaneNames))
	slots := make(chan struct{}, MaxLaneConcurrency)
	var wait sync.WaitGroup
	for index, name := range ReadOnlyLaneNames {
		wait.Add(1)
		go func(index int, name LaneName) {
			defer wait.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				results[index] = LaneResult{
					Lane: name, Verdict: LaneVerdictFail, Summary: "lane cancelled before start",
					Diagnostics: []string{ctx.Err().Error()},
				}
				return
			}
			defer func() { <-slots }()
			laneCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			// The lane runs in its own goroutine so that a lane which ignores
			// cancellation still cannot hold the execution gate open past its
			// deadline. Lanes are bounded, read-only computations, so an
			// abandoned lane finishes without side effects.
			done := make(chan LaneResult, 1)
			go func() {
				if hook != nil {
					hook(laneCtx, name)
				}
				done <- runReadOnlyLane(laneCtx, request, name)
			}()
			var result LaneResult
			select {
			case result = <-done:
				if laneCtx.Err() != nil && result.Verdict == LaneVerdictPass {
					result = laneFailure(name, laneCtx)
				}
			case <-laneCtx.Done():
				result = laneFailure(name, laneCtx)
			}
			results[index] = sanitizeLaneResult(result)
		}(index, name)
	}
	wait.Wait()
	report := LaneReport{
		Version: LaneReportVersion, TaskID: request.TaskID, Verdict: LaneVerdictPass,
		ExecutionGateOpen: true, Lanes: results,
	}
	for _, result := range results {
		if result.Verdict != LaneVerdictPass {
			report.Verdict = LaneVerdictFail
			report.ExecutionGateOpen = false
		}
	}
	return report, nil
}

func laneFailure(name LaneName, laneCtx context.Context) LaneResult {
	summary := "lane did not finish within the per-lane timeout"
	if !errors.Is(laneCtx.Err(), context.DeadlineExceeded) {
		summary = "lane was cancelled before it finished"
	}
	return LaneResult{
		Lane: name, Verdict: LaneVerdictFail, Summary: summary,
		Diagnostics: []string{laneCtx.Err().Error()},
	}
}

func runReadOnlyLane(ctx context.Context, request LaneRequest, name LaneName) LaneResult {
	task, _ := request.Graph.GraphTask(request.TaskID)
	switch name {
	case LaneRepositoryIdentity:
		return laneRepositoryIdentity(task, request)
	case LaneBaselineWorktree:
		return laneBaselineWorktree(task, request)
	case LaneContractConsistency:
		return laneContractConsistency(task, request)
	case LaneHandoffPreparation:
		return laneHandoffPreparation(task, request)
	default:
		return LaneResult{Lane: name, Verdict: LaneVerdictFail, Summary: "unknown read-only lane"}
	}
}

func laneTaskDispatches(graph *core.TaskGraph, taskID string) []core.GraphDispatch {
	dispatches := make([]core.GraphDispatch, 0)
	for _, dispatch := range graph.Dispatches {
		if dispatch.TaskID == taskID {
			dispatches = append(dispatches, dispatch)
		}
	}
	return dispatches
}

func laneLatestDispatch(graph *core.TaskGraph, taskID string) (core.GraphDispatch, bool) {
	dispatches := laneTaskDispatches(graph, taskID)
	if len(dispatches) == 0 {
		return core.GraphDispatch{}, false
	}
	return dispatches[len(dispatches)-1], true
}

// laneDisplayRepository keeps repository diagnostics useful without echoing
// URL userinfo, which may contain a password that core.Redact does not
// recognise as token-shaped.
func laneDisplayRepository(repository string) string {
	parsed, err := url.Parse(repository)
	if err != nil || parsed.User == nil {
		return repository
	}
	parsed.User = url.User("[REDACTED]")
	return parsed.String()
}

func laneRepositoryContainsCredentials(repository string) bool {
	parsed, err := url.Parse(repository)
	if err != nil {
		return strings.Contains(repository, "@")
	}
	if parsed.User != nil {
		_, hasPassword := parsed.User.Password()
		return hasPassword || parsed.User.Username() != ""
	}
	return false
}

func laneRepositoryIdentity(task core.GraphTask, request LaneRequest) LaneResult {
	if strings.TrimSpace(task.Repo) == "" {
		return LaneResult{Lane: LaneRepositoryIdentity, Verdict: LaneVerdictFail, Summary: "Task does not declare a repository"}
	}
	if laneRepositoryContainsCredentials(task.Repo) {
		return LaneResult{
			Lane: LaneRepositoryIdentity, Verdict: LaneVerdictFail,
			Summary: "repository identity contains embedded credentials",
		}
	}
	diagnostics := make([]string, 0)
	for _, dispatch := range laneTaskDispatches(request.Graph, task.ID) {
		if dispatch.Repository != task.Repo {
			diagnostics = append(diagnostics,
				fmt.Sprintf("Dispatch %s repository %q differs from Task repository %q", dispatch.ID, laneDisplayRepository(dispatch.Repository), laneDisplayRepository(task.Repo)))
		}
		if dispatch.Instruction.Repository != task.Repo {
			diagnostics = append(diagnostics,
				fmt.Sprintf("Dispatch %s instruction repository %q differs from Task repository %q", dispatch.ID, laneDisplayRepository(dispatch.Instruction.Repository), laneDisplayRepository(task.Repo)))
		}
	}
	if len(diagnostics) != 0 {
		return LaneResult{Lane: LaneRepositoryIdentity, Verdict: LaneVerdictFail, Summary: "repository identity is inconsistent", Diagnostics: diagnostics}
	}
	return LaneResult{Lane: LaneRepositoryIdentity, Verdict: LaneVerdictPass, Summary: "repository identity is consistent"}
}

var laneSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func laneBaselineWorktree(task core.GraphTask, request LaneRequest) LaneResult {
	diagnostics := make([]string, 0)
	if !laneSHAPattern.MatchString(task.BaselineSHA) {
		diagnostics = append(diagnostics, "Task baseline SHA is not a full 40- or 64-digit hexadecimal SHA")
	}
	if strings.TrimSpace(task.Branch) == "" || task.Branch == "HEAD" {
		diagnostics = append(diagnostics, "Task branch is not an explicit branch name")
	}
	if task.Worktree != "" && !filepath.IsAbs(task.Worktree) {
		diagnostics = append(diagnostics, "Task worktree is not an absolute path")
	}
	if request.RepositoryRoot != "" && task.Worktree != "" && filepath.IsAbs(task.Worktree) {
		root, err := filepath.Abs(request.RepositoryRoot)
		if err != nil {
			diagnostics = append(diagnostics, "repository root cannot be resolved")
		} else {
			worktree := filepath.Clean(task.Worktree)
			relative, err := filepath.Rel(root, worktree)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				diagnostics = append(diagnostics, "Task worktree is outside the repository root")
			} else if relative == "." {
				diagnostics = append(diagnostics, "Task worktree equals the repository root")
			}
		}
	}
	if task.Worktree != "" {
		if info, err := os.Stat(task.Worktree); err != nil || !info.IsDir() {
			diagnostics = append(diagnostics, "Task worktree is not an existing directory")
		}
	}
	for _, dispatch := range laneTaskDispatches(request.Graph, task.ID) {
		instruction := dispatch.Instruction
		if instruction.Worktree != task.Worktree || instruction.Branch != task.Branch || instruction.BaselineSHA != task.BaselineSHA {
			diagnostics = append(diagnostics,
				fmt.Sprintf("Dispatch %s baseline, branch or worktree differs from the Task", dispatch.ID))
		}
	}
	if len(diagnostics) != 0 {
		return LaneResult{Lane: LaneBaselineWorktree, Verdict: LaneVerdictFail, Summary: "baseline or worktree is not execution-safe", Diagnostics: diagnostics}
	}
	return LaneResult{Lane: LaneBaselineWorktree, Verdict: LaneVerdictPass, Summary: "baseline, branch and worktree are consistent"}
}

func laneContractConsistency(task core.GraphTask, request LaneRequest) LaneResult {
	diagnostics := make([]string, 0)
	if strings.TrimSpace(task.Objective) == "" {
		diagnostics = append(diagnostics, "Task objective is empty")
	}
	if len(task.Scope) == 0 {
		diagnostics = append(diagnostics, "Task scope is empty")
	}
	if len(task.Acceptance) == 0 {
		diagnostics = append(diagnostics, "Task acceptance criteria are empty")
	}
	if len(task.RequiredEvidence) == 0 {
		diagnostics = append(diagnostics, "Task required evidence is empty")
	}
	dispatch, ok := laneLatestDispatch(request.Graph, task.ID)
	if ok {
		instruction := dispatch.Instruction
		fields := make([]string, 0)
		if instruction.TaskID != task.ID {
			fields = append(fields, "taskId")
		}
		if instruction.DispatchTo != task.Owner {
			fields = append(fields, "dispatchTo")
		}
		if instruction.Objective != task.Objective {
			fields = append(fields, "objective")
		}
		if !equalLaneStrings(instruction.AuthorizedActions, task.Scope) {
			fields = append(fields, "scope")
		}
		if !equalLaneStrings(instruction.ForbiddenActions, task.OutOfScope) {
			fields = append(fields, "outOfScope")
		}
		if !equalLaneStrings(instruction.Acceptance, task.Acceptance) {
			fields = append(fields, "acceptance")
		}
		if !equalLaneStrings(instruction.RequiredEvidence, task.RequiredEvidence) {
			fields = append(fields, "requiredEvidence")
		}
		if len(fields) != 0 {
			diagnostics = append(diagnostics,
				fmt.Sprintf("latest Dispatch %s differs from the Task contract in %s", dispatch.ID, strings.Join(fields, ", ")))
		}
	}
	if len(diagnostics) != 0 {
		return LaneResult{Lane: LaneContractConsistency, Verdict: LaneVerdictFail, Summary: "Task and Dispatch contracts are inconsistent", Diagnostics: diagnostics}
	}
	return LaneResult{Lane: LaneContractConsistency, Verdict: LaneVerdictPass, Summary: "Task and Dispatch contracts agree"}
}

func equalLaneStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func laneDispatchActive(status core.DispatchStatus) bool {
	switch status {
	case core.DispatchIssued, core.DispatchAcknowledged, core.DispatchRunning,
		core.DispatchCancelRequested, core.DispatchSuspended:
		return true
	default:
		return false
	}
}

func laneHandoffPreparation(task core.GraphTask, request LaneRequest) LaneResult {
	diagnostics := make([]string, 0)
	dispatch, ok := laneLatestDispatch(request.Graph, task.ID)
	if !ok {
		diagnostics = append(diagnostics, "no Dispatch has been issued for the Task")
	} else {
		if !laneDispatchActive(dispatch.Status) {
			diagnostics = append(diagnostics, fmt.Sprintf("latest Dispatch %s is not active", dispatch.ID))
		}
		if strings.TrimSpace(dispatch.Recipient) == "" {
			diagnostics = append(diagnostics, fmt.Sprintf("Dispatch %s has no role recipient", dispatch.ID))
		}
		if strings.TrimSpace(dispatch.InstructionHash) == "" {
			diagnostics = append(diagnostics, fmt.Sprintf("Dispatch %s has no instruction hash", dispatch.ID))
		}
		if len(dispatch.Instruction.ReturnFormat) == 0 {
			diagnostics = append(diagnostics, fmt.Sprintf("Dispatch %s has no return format", dispatch.ID))
		}
		for _, flow := range request.Graph.ReviewFlows {
			if flow.TaskID != task.ID || flow.Status != "RUNNING" {
				continue
			}
			if flow.HandoffHeld() {
				diagnostics = append(diagnostics, fmt.Sprintf("review flow %s holds the handoff", flow.ID))
			}
			for _, id := range flow.DispatchIDs {
				if _, exists := request.Graph.GraphDispatch(id); !exists {
					diagnostics = append(diagnostics, fmt.Sprintf("review flow %s references unknown Dispatch %s", flow.ID, id))
				}
			}
			if len(flow.DispatchIDs) != 0 && flow.DispatchIDs[len(flow.DispatchIDs)-1] != dispatch.ID {
				diagnostics = append(diagnostics, fmt.Sprintf("latest Dispatch %s is not the current Dispatch of review flow %s", dispatch.ID, flow.ID))
			}
		}
	}
	if len(diagnostics) != 0 {
		return LaneResult{Lane: LaneHandoffPreparation, Verdict: LaneVerdictFail, Summary: "handoff is not ready for the next role", Diagnostics: diagnostics}
	}
	return LaneResult{Lane: LaneHandoffPreparation, Verdict: LaneVerdictPass, Summary: "handoff packet is prepared"}
}

func sanitizeLaneResult(result LaneResult) LaneResult {
	result.Summary = core.Redact(result.Summary)
	if len(result.Diagnostics) != 0 {
		diagnostics := make([]string, 0, len(result.Diagnostics))
		for _, diagnostic := range result.Diagnostics {
			diagnostics = append(diagnostics, core.Redact(diagnostic))
		}
		result.Diagnostics = diagnostics
	}
	return result
}
