package controller

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// RequestProposal contains advice only; identity, configuration and budgets
// are supplied by the host, not chosen by the Planner.
type RequestProposal struct {
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	Scope              []string `json:"scope"`
	OutOfScope         []string `json:"outOfScope"`
	Acceptance         []string `json:"acceptance"`
	ValidationCommands []string `json:"validationCommands"`
}

func ValidateRequestPlan(answer QuestionAnswer) error {
	if answer.Recommendation != "HOLD" && answer.Recommendation != "HUMAN_REQUIRED" {
		return fmt.Errorf("taxion: request Planner must hold a proposal or ask for Human clarification")
	}
	if answer.Questions == nil {
		return fmt.Errorf("taxion: request Planner must return an explicit questions list")
	}
	for _, question := range answer.Questions {
		if strings.TrimSpace(question) == "" {
			return fmt.Errorf("taxion: request Planner returned an empty question")
		}
	}
	if answer.Proposal == nil {
		if len(answer.Questions) == 0 {
			return fmt.Errorf("taxion: request Planner returned neither questions nor a proposal")
		}
		return nil
	}
	proposal := answer.Proposal
	if strings.TrimSpace(proposal.Title) == "" || strings.TrimSpace(proposal.Objective) == "" ||
		len(proposal.Scope) == 0 || len(proposal.Acceptance) == 0 || len(proposal.ValidationCommands) == 0 {
		return fmt.Errorf("taxion: request proposal requires a title, objective, scope, acceptance and validation commands")
	}
	for _, values := range [][]string{proposal.Scope, proposal.OutOfScope, proposal.Acceptance, proposal.ValidationCommands} {
		seen := map[string]bool{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || seen[value] {
				return fmt.Errorf("taxion: request proposal contains empty or duplicate values")
			}
			seen[value] = true
		}
	}
	for _, path := range append(append([]string{}, proposal.Scope...), proposal.OutOfScope...) {
		if filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path ||
			path == "." || path == ".." || strings.HasPrefix(path, "../") ||
			strings.ContainsAny(path, "\\\x00\r\n") {
			return fmt.Errorf("taxion: request proposal scope must use repository-relative paths or patterns")
		}
	}
	for _, command := range proposal.ValidationCommands {
		if strings.ContainsAny(command, "\x00\r\n") {
			return fmt.Errorf("taxion: request proposal validation must be a single shell command line")
		}
	}
	// Apply the existing graph's contract checks without issuing work.
	graph := core.TaskGraph{Version: 1}
	return graph.Track(core.GraphTask{
		ID: "proposal", Title: proposal.Title, Repo: "proposal/repository",
		Type: "IMPLEMENTATION", Status: core.Draft, CreatedFrom: "planner:proposal",
		Issue: "NOT_REQUIRED: unapproved planning proposal",
		Scope: proposal.Scope, OutOfScope: proposal.OutOfScope,
		Acceptance: proposal.Acceptance, ValidationCommands: proposal.ValidationCommands,
	}, "planner", "Validate an unapproved proposal")
}
