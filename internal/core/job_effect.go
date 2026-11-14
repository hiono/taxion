package core

import (
	"path/filepath"
	"slices"
	"strings"
)

// JobEffectTarget identifies one resource touched by an observed action.
// Path is repository-relative and must already be canonical.
type JobEffectTarget struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Tracked    bool   `json:"tracked,omitempty"`
}

// JobEffectObservation describes effects observed at the provider boundary.
// Inspectable and SandboxEnforced are evidence supplied by the trusted adapter,
// not claims inferred from tool names or natural-language descriptions.
type JobEffectObservation struct {
	Effects         []JobEffect       `json:"effects"`
	Targets         []JobEffectTarget `json:"targets,omitempty"`
	Command         string            `json:"command,omitempty"`
	Inspectable     bool              `json:"inspectable"`
	SandboxEnforced bool              `json:"sandboxEnforced"`
	SandboxBypass   bool              `json:"sandboxBypass,omitempty"`
}

// JobEffectDecision is the result of evaluating an observed action.
type JobEffectDecision string

const (
	JobEffectAllow           JobEffectDecision = "allow"
	JobEffectRequireApproval JobEffectDecision = "require_approval"
	JobEffectDeny            JobEffectDecision = "deny"
)

// EvaluateJobEffect applies the approved Job contract to an observed action.
// Unknown or incomplete effects fail closed. Ordinary resource access is
// authorized by the Job repository/file boundary, not by operation names.
func (c JobContract) EvaluateJobEffect(observation JobEffectObservation) JobEffectDecision {
	if c.Validate() != nil || !observation.Inspectable || len(observation.Effects) == 0 ||
		!validJobEffectTargets(observation.Targets) || observation.SandboxBypass {
		return JobEffectDeny
	}

	targetIDs := make([]string, 0, len(observation.Targets)+1)
	for _, target := range observation.Targets {
		targetIDs = append(targetIDs, target.Repository+":"+target.Path)
	}
	if observation.Command != "" {
		targetIDs = append(targetIDs, "command:"+observation.Command)
	}

	decision := JobEffectAllow
	for _, effect := range observation.Effects {
		if !validJobEffect(effect) || jobEffectRuleMatches(c.ProhibitedEffects, effect, targetIDs) {
			return JobEffectDeny
		}
		switch effect {
		case JobEffectRepositoryRead, JobEffectRepositoryWrite:
			if !observation.SandboxEnforced || !jobEffectTargetsWithinContract(c, observation.Targets) {
				return JobEffectDeny
			}
		case JobEffectValidationExecution:
			if !observation.SandboxEnforced || observation.Command == "" ||
				!slices.Contains(c.ValidationCommands, observation.Command) {
				return JobEffectDeny
			}
			if len(observation.Targets) != 0 && !jobEffectTargetsWithinContract(c, observation.Targets) {
				return JobEffectDeny
			}
		default:
			// Every other recognized effect must be explicitly prohibited or
			// approval-required. A newly added effect cannot become implicitly
			// executable merely because its identifier is syntactically valid.
			if jobEffectRuleMatches(c.ApprovalRequiredEffects, effect, targetIDs) {
				decision = JobEffectRequireApproval
				continue
			}
			return JobEffectDeny
		}
		if jobEffectRuleMatches(c.ApprovalRequiredEffects, effect, targetIDs) {
			decision = JobEffectRequireApproval
		}
	}
	return decision
}

func validCanonicalJobEffectPath(target string) bool {
	path := filepath.FromSlash(target)
	return strings.TrimSpace(target) != "" &&
		!filepath.IsAbs(path) &&
		filepath.VolumeName(path) == "" &&
		filepath.ToSlash(filepath.Clean(path)) == target &&
		target != "." && target != ".." && !strings.HasPrefix(target, "../") &&
		target != ".git" && !strings.HasPrefix(target, ".git/") &&
		!ProhibitedArtifactPath(target)
}

func validJobEffectTargets(targets []JobEffectTarget) bool {
	seen := map[string]bool{}
	for _, target := range targets {
		if strings.TrimSpace(target.Repository) == "" || !validCanonicalJobEffectPath(target.Path) {
			return false
		}
		key := target.Repository + ":" + target.Path
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func jobEffectTargetsWithinContract(contract JobContract, targets []JobEffectTarget) bool {
	if len(targets) == 0 {
		return false
	}
	for _, target := range targets {
		var boundary *JobRepositoryScope
		for i := range contract.Repositories {
			if contract.Repositories[i].Repository == target.Repository {
				boundary = &contract.Repositories[i]
				break
			}
		}
		if boundary == nil {
			return false
		}
		if boundary.FileAccess.Level == FileAccessCommitted && !target.Tracked {
			return false
		}
		candidate := FileAccessPolicy{Level: FileAccessExact, Paths: []string{target.Path}}
		if !FileAccessPolicyWithin(candidate, boundary.FileAccess) {
			return false
		}
	}
	return true
}

func jobEffectRuleMatches(rules []JobEffectRule, effect JobEffect, targetIDs []string) bool {
	for _, rule := range rules {
		if rule.Effect != effect {
			continue
		}
		if len(rule.Targets) == 0 {
			return true
		}
		for _, target := range targetIDs {
			if slices.Contains(rule.Targets, target) {
				return true
			}
		}
	}
	return false
}
