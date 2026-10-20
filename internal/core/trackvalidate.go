package core

import (
	"regexp"
	"slices"
	"strings"
)

// CredentialWithheld is reported instead of echoing a value that looks like a
// credential; the value itself is never included in any output.
const CredentialWithheld = "credential-like value in the contract; the value is withheld"

var taskTypes = []string{"INVESTIGATION", "DESIGN", "IMPLEMENTATION", "REVIEW", "EVIDENCE_COLLECTION"}

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`),
	regexp.MustCompile(`\bBearer [A-Za-z0-9._\-]{20,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.`),
}

// TrackValidation is the result of a save-free Task registration check. It
// reports what task track would do without touching the workspace.
type TrackValidation struct {
	TaskID          string   `json:"taskId"`
	Valid           bool     `json:"valid"`
	ExistingTask    bool     `json:"existingTask"`
	StateChanges    string   `json:"stateChanges"`
	ValidatorErrors []string `json:"validatorErrors"`
	DigestWithheld  bool     `json:"digestWithheld,omitempty"`
	CanonicalJSON   string   `json:"canonicalJson,omitempty"`
	CanonicalSHA256 string   `json:"canonicalSha256,omitempty"`
}

// ValidateTracking applies the same rules as task track on a copy of the graph
// and discards the copy. Contract-level errors are collected so that a caller
// sees every independent violation at once instead of only the first one.
// graphInitialized mirrors the workspace state that task track would migrate.
// Passing no ledger skips the ledger projection check that track performs.
func (g TaskGraph) ValidateTracking(t GraphTask, actor, reason string, concern *Concern, ledger Ledger, graphInitialized bool) TrackValidation {
	report := TrackValidation{TaskID: redactValue(t.ID), StateChanges: "none", ValidatorErrors: []string{}}
	next := cloneGraph(g)
	_, report.ExistingTask = next.task(t.ID)

	candidate := cloneTask(t)
	for _, message := range taskContractErrors(next, candidate) {
		report.add(message)
	}
	if err := next.trackInto(candidate, actor, reason); err != nil {
		report.add(err.Error())
		next.Tasks = append(next.Tasks, candidate)
		if concern != nil {
			for _, message := range concernContractErrors(next, *concern) {
				report.add(message)
			}
		}
	} else {
		if !graphInitialized {
			if err := GuardActiveDispatch(ledger.Dispatches); err != nil {
				report.add("resolve legacy active dispatches before graph migration: " + err.Error())
			}
		}
		if err := next.CheckLedger(ledger); err != nil {
			report.add(err.Error())
		}
		if concern != nil {
			if err := next.AddConcern(*concern); err != nil {
				report.add(err.Error())
			}
		}
	}

	if len(report.ValidatorErrors) == 0 {
		if stored, exists := next.GraphTask(candidate.ID); !exists {
			report.add("validated contract is missing from the candidate graph")
		} else if data, err := CanonicalJSON(stored); err != nil {
			report.add("canonical encoding failed: " + err.Error())
		} else if credentialLike(string(data)) {
			report.DigestWithheld = true
		} else {
			report.CanonicalJSON = string(data)
			report.CanonicalSHA256 = SHA256Hex(data)
		}
	}

	report.Valid = len(report.ValidatorErrors) == 0
	return report
}

func (r *TrackValidation) add(message string) {
	if message == "" {
		return
	}
	message = redactValue(message)
	for _, existing := range r.ValidatorErrors {
		if existing == message {
			return
		}
	}
	r.ValidatorErrors = append(r.ValidatorErrors, message)
}

// taskContractErrors mirrors the per-task rules of TaskGraph.Validate for the
// candidate task so that every independent violation is reported together.
// Rules that need the whole graph after the update stay in trackInto, which
// runs the complete validator.
func taskContractErrors(g TaskGraph, t GraphTask) []string {
	var errors []string
	add := func(format string, args ...any) {
		errors = append(errors, graphError(format, args...).Error())
	}
	if !nonempty(t.Title) || !nonempty(t.CreatedFrom) || len(strings.Split(t.Repo, "/")) < 2 ||
		strings.ContainsAny(t.Repo, " \t\r\n\\:?#%") {
		add("task %s requires title, source and namespace/repository", t.ID)
	}
	for _, segment := range strings.Split(t.Repo, "/") {
		if segment == "" || segment == "." || segment == ".." {
			add("task %s requires nonempty canonical repository path segments", t.ID)
			break
		}
	}
	if !slices.Contains(taskTypes, t.Type) {
		add("invalid type %q for %s", t.Type, t.ID)
	}
	if !validStatus(t.Status) {
		add("invalid status %q for %s", t.Status, t.ID)
	}
	if !validIssue(t.Issue) {
		add("task %s requires an Issue reference or explicit NOT_REQUIRED reason", t.ID)
	}
	for _, field := range []struct {
		name   string
		values []string
	}{
		{"scope", t.Scope}, {"outOfScope", t.OutOfScope}, {"acceptance", t.Acceptance},
		{"requiredEvidence", t.RequiredEvidence}, {"validationCommands", t.ValidationCommands},
		{"exceptionIds", t.ExceptionIDs}, {"memoryRefs", t.MemoryRefs},
	} {
		if !validList(field.values) {
			add("empty or duplicate contract value in %s for %s", field.name, t.ID)
		}
	}
	for _, ref := range t.MemoryRefs {
		if !validMemoryReference(ref) {
			add("invalid Memory reference %q for %s", ref, t.ID)
		}
	}
	if err := validateSpecifications(t.Specifications); err != nil {
		errors = append(errors, err.Error())
	}
	decisionIDs := map[string]TaskDecision{}
	for _, task := range g.Tasks {
		if task.ID == t.ID {
			continue
		}
		for _, decision := range task.Decisions {
			decisionIDs[decision.ID] = decision
		}
	}
	for _, decision := range t.Decisions {
		if !nonempty(decision.ID) || !nonempty(decision.Source) || !nonempty(decision.Text) {
			add("invalid decision %q for %s", decision.ID, t.ID)
			continue
		}
		if previous, ok := decisionIDs[decision.ID]; ok && previous != decision {
			add("conflicting decision %s", decision.ID)
		}
	}
	if t.FileAccess != nil {
		if err := t.FileAccess.Validate(); err != nil {
			add("invalid file access policy for %s: %v", t.ID, err)
		}
	}
	if err := t.BudgetGuidance.Validate(); err != nil {
		add("invalid budget guidance for %s: %v", t.ID, err)
	}
	if !validOutputMode(t.OutputMode) ||
		(t.OutputMode != OutputModeWorktreeTreeSnapshot &&
			(t.TargetTreeSHA != "" || len(t.TargetChangedFiles) != 0)) ||
		(t.TargetTreeSHA != "" && !validSHA(t.TargetTreeSHA)) ||
		(t.OutputMode == OutputModeWorktreeTreeSnapshot && t.Owner == "REVIEWER" &&
			!validSHA(t.TargetTreeSHA)) ||
		!validList(t.TargetChangedFiles) {
		add("invalid output mode or target tree for %s", t.ID)
	}
	if err := validateHumanDecision(t); err != nil {
		errors = append(errors, err.Error())
	}
	if t.DeferredReason != "" && ((t.Status != Blocked && !terminal(t.Status)) || !nonempty(t.ResumeCondition) || !nonempty(t.NextAction)) {
		add("deferred task %s requires BLOCKED, next action and resume condition", t.ID)
	}
	if strings.HasPrefix(t.Issue, "PENDING:") {
		pending := strings.TrimSpace(strings.TrimPrefix(t.Issue, "PENDING:"))
		registered := false
		for _, dep := range t.DependsOn {
			if dep.TaskID == pending && dep.State == Completed {
				registered = true
			}
		}
		_, known := g.GraphTask(pending)
		if (!known && pending != t.ID) || !registered {
			add("pending Issue requires a registration task and completion dependency")
		}
	}
	for _, ref := range []string{t.ParentID, t.Supersedes} {
		if ref != "" {
			if _, exists := g.GraphTask(ref); !exists || ref == t.ID {
				add("invalid relationship %s -> %s", t.ID, ref)
			}
		}
	}
	seen := map[string]bool{}
	for _, dep := range t.DependsOn {
		if dep.TaskID == t.ID || seen[dep.TaskID] {
			add("invalid dependency %q for %s", dep.TaskID, t.ID)
			continue
		}
		seen[dep.TaskID] = true
		if _, exists := g.GraphTask(dep.TaskID); !exists {
			add("invalid dependency %q for %s", dep.TaskID, t.ID)
			continue
		}
		switch dep.State {
		case Approved, Completed:
		default:
			add("invalid dependency %q for %s", dep.TaskID, t.ID)
		}
	}
	return errors
}

// concernContractErrors mirrors the concern rules of TaskGraph.Validate so that
// an independent concern violation is still reported when the task contract
// itself is rejected.
func concernContractErrors(g TaskGraph, c Concern) []string {
	var errors []string
	_, known := g.GraphTask(c.TaskID)
	invalid := !nonempty(c.ID) || !nonempty(c.Source) || !nonempty(c.Text) || !known
	for _, existing := range g.Concerns {
		if c.ID != "" && existing.ID == c.ID {
			invalid = true
		}
	}
	if invalid {
		errors = append(errors, graphError("Task lost: concern %q requires source, text and registered task ID", c.ID).Error())
	}
	return errors
}

// Redact replaces credential-like substrings in text so that diagnostics can
// be printed without echoing a secret.
func Redact(text string) string { return redactValue(text) }

func credentialLike(value string) bool {
	for _, pattern := range credentialPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

// redactValue removes credential-like substrings so that diagnostics can never
// echo a secret, even when the contract itself is rejected.
func redactValue(value string) string {
	for _, pattern := range credentialPatterns {
		value = pattern.ReplaceAllString(value, "[REDACTED]")
	}
	return value
}
