package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

type TaskStatus string

const (
	Draft                 TaskStatus = "DRAFT"
	Ready                 TaskStatus = "READY"
	Active                TaskStatus = "ACTIVE"
	WaitingForEvidence    TaskStatus = "WAITING_FOR_EVIDENCE"
	WaitingForReview      TaskStatus = "WAITING_FOR_REVIEW"
	RequestChanges        TaskStatus = "REQUEST_CHANGES"
	Blocked               TaskStatus = "BLOCKED"
	HumanDecisionRequired TaskStatus = "HUMAN_DECISION_REQUIRED"
	Approved              TaskStatus = "APPROVED"
	Completed             TaskStatus = "COMPLETED"
	Rejected              TaskStatus = "REJECTED"
	Cancelled             TaskStatus = "CANCELLED"
	Superseded            TaskStatus = "SUPERSEDED"
)

var ErrGraphRejected = errors.New("task graph rejected")

type TaskDependency struct {
	TaskID string     `json:"taskId"`
	State  TaskStatus `json:"state"`
}

type TaskDecision struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// GraphTask extends the execution model without changing the legacy Task wire.
// Parent/children, siblings and blocks are derived from parentId and dependsOn.
type GraphTask struct {
	ID               string           `json:"id"`
	Title            string           `json:"title"`
	Repo             string           `json:"repo"`
	Type             string           `json:"type"`
	Issue            string           `json:"issue"`
	ChangeRef        string           `json:"changeRef"`
	ParentID         string           `json:"parentId"`
	DependsOn        []TaskDependency `json:"dependsOn"`
	Supersedes       string           `json:"supersedes"`
	Owner            string           `json:"owner"`
	Reviewer         string           `json:"reviewer"`
	Status           TaskStatus       `json:"status"`
	Priority         string           `json:"priority"`
	Objective        string           `json:"objective"`
	Scope            []string         `json:"scope"`
	OutOfScope       []string         `json:"outOfScope"`
	Acceptance       []string         `json:"acceptance"`
	RequiredEvidence []string         `json:"requiredEvidence"`
	Branch           string           `json:"branch"`
	BaselineSHA      string           `json:"baselineSha"`
	TargetSHA        string           `json:"targetSha"`
	Risk             string           `json:"risk"`
	ExceptionIDs     []string         `json:"exceptionIds"`
	CreatedFrom      string           `json:"createdFrom"`
	NextAction       string           `json:"nextAction"`
	ExitCondition    string           `json:"exitCondition"`
	DeferredReason   string           `json:"deferredReason"`
	ResumeCondition  string           `json:"resumeCondition"`
	AuthorizationID  string           `json:"authorizationId"`
	Decisions        []TaskDecision   `json:"decisions"`
	MemoryRefs       []string         `json:"memoryRefs"`
}

type Concern struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
	TaskID string `json:"taskId"`
}

type GraphEvent struct {
	TaskID string           `json:"taskId"`
	Actor  string           `json:"actor"`
	Reason string           `json:"reason"`
	At     time.Time        `json:"at"`
	Task   GraphTask        `json:"task"`
	Proof  *TransitionProof `json:"proof,omitempty"`
}

type TransitionProof struct {
	ActionPerformed bool         `json:"actionPerformed"`
	Evidence        []string     `json:"evidence"`
	Acceptance      []string     `json:"acceptance"`
	ReviewedSHA     string       `json:"reviewedSha"`
	Reviewer        string       `json:"reviewer"`
	OutputSHA       string       `json:"outputSha"`
	Waivers         []TaskWaiver `json:"waivers"`
}

type TaskWaiver struct {
	DecisionID string   `json:"decisionId"`
	Acceptance []string `json:"acceptance"`
	Evidence   []string `json:"evidence"`
	Children   []string `json:"children"`
}

type TaskGraph struct {
	Version  int          `json:"version"`
	Tasks    []GraphTask  `json:"tasks"`
	Concerns []Concern    `json:"concerns"`
	History  []GraphEvent `json:"history"`
}

func graphError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrGraphRejected, fmt.Sprintf(format, args...))
}

func (g TaskGraph) task(id string) (GraphTask, bool) {
	for _, t := range g.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return GraphTask{}, false
}

func validStatus(s TaskStatus) bool {
	return slices.Contains([]TaskStatus{Draft, Ready, Active, WaitingForEvidence, WaitingForReview,
		RequestChanges, Blocked, HumanDecisionRequired, Approved, Completed, Rejected, Cancelled, Superseded}, s)
}

func terminal(s TaskStatus) bool {
	return slices.Contains([]TaskStatus{Completed, Rejected, Cancelled, Superseded}, s)
}

func nonempty(s string) bool { return strings.TrimSpace(s) != "" }

func validList(values []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if !nonempty(v) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

func validIssue(ref string) bool {
	if strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		return err == nil && u.Host != "" && u.Path != ""
	}
	for _, prefix := range []string{"Issue #", "INHERITED: Issue #"} {
		if strings.HasPrefix(ref, prefix) {
			n, err := strconv.Atoi(strings.TrimPrefix(ref, prefix))
			return err == nil && n > 0
		}
	}
	for _, prefix := range []string{"NOT_REQUIRED:", "PENDING:"} {
		if strings.HasPrefix(ref, prefix) {
			return nonempty(strings.TrimPrefix(ref, prefix))
		}
	}
	return false
}

func (g TaskGraph) Validate() error {
	if g.Version != 1 {
		return graphError("unsupported version %d", g.Version)
	}
	ids := map[string]bool{}
	decisionIDs := map[string]TaskDecision{}
	for _, t := range g.Tasks {
		if !nonempty(t.ID) || ids[t.ID] {
			return graphError("missing or duplicate task ID %q", t.ID)
		}
		ids[t.ID] = true
		if !nonempty(t.Title) || !nonempty(t.CreatedFrom) || len(strings.Split(t.Repo, "/")) != 2 ||
			strings.ContainsAny(t.Repo, " \t\n") || strings.HasPrefix(t.Repo, "/") || strings.HasSuffix(t.Repo, "/") {
			return graphError("task %s requires title, source and namespace/repository", t.ID)
		}
		if !slices.Contains([]string{"INVESTIGATION", "DESIGN", "IMPLEMENTATION", "REVIEW", "EVIDENCE_COLLECTION"}, t.Type) || !validStatus(t.Status) {
			return graphError("invalid type or status for %s", t.ID)
		}
		if !validIssue(t.Issue) {
			return graphError("task %s requires an Issue reference or explicit NOT_REQUIRED reason", t.ID)
		}
		for _, values := range [][]string{t.Scope, t.OutOfScope, t.Acceptance, t.RequiredEvidence, t.ExceptionIDs, t.MemoryRefs} {
			if !validList(values) {
				return graphError("empty or duplicate contract value for %s", t.ID)
			}
		}
		for _, d := range t.Decisions {
			if !nonempty(d.ID) || !nonempty(d.Source) || !nonempty(d.Text) {
				return graphError("invalid decision for %s", t.ID)
			}
			if previous, ok := decisionIDs[d.ID]; ok && previous != d {
				return graphError("conflicting decision %s", d.ID)
			}
			decisionIDs[d.ID] = d
		}
		if t.DeferredReason != "" && ((t.Status != Blocked && !terminal(t.Status)) || !nonempty(t.ResumeCondition) || !nonempty(t.NextAction)) {
			return graphError("deferred task %s requires BLOCKED, next action and resume condition", t.ID)
		}
	}
	for _, t := range g.Tasks {
		if strings.HasPrefix(t.Issue, "PENDING:") {
			pending := strings.TrimSpace(strings.TrimPrefix(t.Issue, "PENDING:"))
			registered := false
			for _, dep := range t.DependsOn {
				if dep.TaskID == pending && dep.State == Completed {
					registered = true
				}
			}
			if !ids[pending] || !registered {
				return graphError("pending Issue requires a registration task and completion dependency")
			}
		}
		for _, ref := range []string{t.ParentID, t.Supersedes} {
			if ref != "" && (!ids[ref] || ref == t.ID) {
				return graphError("invalid relationship %s -> %s", t.ID, ref)
			}
		}
		seen := map[string]bool{}
		for _, dep := range t.DependsOn {
			if !ids[dep.TaskID] || dep.TaskID == t.ID || seen[dep.TaskID] || !slices.Contains([]TaskStatus{Approved, Completed}, dep.State) {
				return graphError("invalid dependency for %s", t.ID)
			}
			seen[dep.TaskID] = true
		}
	}
	for _, relation := range []string{"dependencies", "parent", "supersedes", "completion"} {
		done, visiting := map[string]bool{}, map[string]bool{}
		var visit func(string) error
		visit = func(id string) error {
			if visiting[id] {
				return graphError("%s cycle at %s", relation, id)
			}
			if done[id] {
				return nil
			}
			visiting[id] = true
			t, _ := g.task(id)
			var refs []string
			switch relation {
			case "dependencies":
				for _, dep := range t.DependsOn {
					refs = append(refs, dep.TaskID)
				}
			case "parent":
				if t.ParentID != "" {
					refs = append(refs, t.ParentID)
				}
			case "supersedes":
				if t.Supersedes != "" {
					refs = append(refs, t.Supersedes)
				}
			case "completion":
				for _, dependency := range t.DependsOn {
					refs = append(refs, dependency.TaskID)
				}
				for _, child := range g.Tasks {
					if child.ParentID == id {
						refs = append(refs, child.ID)
					}
				}
			}
			for _, ref := range refs {
				if err := visit(ref); err != nil {
					return err
				}
			}
			visiting[id] = false
			done[id] = true
			return nil
		}
		for id := range ids {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	concerns := map[string]bool{}
	for _, c := range g.Concerns {
		if !nonempty(c.ID) || concerns[c.ID] || !nonempty(c.Source) || !nonempty(c.Text) || !ids[c.TaskID] {
			return graphError("Task lost: concern %q requires source, text and registered task ID", c.ID)
		}
		concerns[c.ID] = true
	}
	for _, t := range g.Tasks {
		if slices.Contains([]TaskStatus{Ready, Active, WaitingForEvidence, WaitingForReview, RequestChanges, Approved, Completed}, t.Status) {
			if err := g.executable(t); err != nil {
				return err
			}
		}
	}
	latest := map[string]GraphTask{}
	for _, e := range g.History {
		if !ids[e.TaskID] || e.Task.ID != e.TaskID || !nonempty(e.Actor) || !nonempty(e.Reason) || e.At.IsZero() {
			return graphError("invalid history event")
		}
		if previous, ok := latest[e.TaskID]; ok {
			if previous.Repo != e.Task.Repo || previous.CreatedFrom != e.Task.CreatedFrom {
				return graphError("task identity changed in history")
			}
			for _, d := range previous.Decisions {
				if !slices.Contains(e.Task.Decisions, d) {
					return graphError("decision removed from history")
				}
			}
			if previous.Status != e.Task.Status {
				if !allowedTransition(previous.Status, e.Task.Status) {
					return graphError("invalid historical transition")
				}
			} else if terminal(previous.Status) {
				return graphError("terminal task changed in history")
			}
		} else if e.Task.Status != Draft && e.Task.Status != Blocked {
			return graphError("task history must start at DRAFT or BLOCKED")
		}
		latest[e.TaskID] = e.Task
		if e.Task.Status == Approved || e.Task.Status == Completed {
			if e.Proof == nil {
				return graphError("missing completion proof for %s", e.TaskID)
			}
			if err := completionProof(e.Task, *e.Proof); err != nil {
				return err
			}
		}
	}
	for _, t := range g.Tasks {
		snapshot, ok := latest[t.ID]
		if !ok || !equalJSON(snapshot, t) {
			return graphError("task %s does not match its history", t.ID)
		}
		if t.Status == Approved || t.Status == Completed {
			var proof TransitionProof
			for _, event := range g.History {
				if event.TaskID == t.ID && event.Proof != nil {
					proof = *event.Proof
				}
			}
			for _, child := range g.Tasks {
				if child.ParentID == t.ID && child.Status != Completed && !waived(proof, child.ID, "child") {
					return graphError("parent %s has an incomplete child %s", t.ID, child.ID)
				}
			}
		}
	}
	return nil
}

func equalJSON(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

func (g TaskGraph) executable(t GraphTask) error {
	if t.DeferredReason != "" || !nonempty(t.AuthorizationID) || !nonempty(t.Objective) ||
		len(t.Scope) == 0 || len(t.Acceptance) == 0 || len(t.RequiredEvidence) == 0 || !nonempty(t.ExitCondition) ||
		(t.Owner != "CODER" && t.Owner != "REVIEWER") || strings.HasPrefix(t.Issue, "PENDING:") {
		return graphError("task %s lacks authorization, execution fields or Issue registration", t.ID)
	}
	found := false
	for _, d := range t.Decisions {
		if d.ID == t.AuthorizationID {
			found = true
		}
	}
	if !found {
		return graphError("task %s lacks its authorization decision", t.ID)
	}
	for _, dep := range t.DependsOn {
		other, _ := g.task(dep.TaskID)
		if other.Status != dep.State && !(dep.State == Approved && other.Status == Completed) {
			return graphError("task %s dependency %s requires %s (has %s)", t.ID, dep.TaskID, dep.State, other.Status)
		}
	}
	return nil
}

func (g TaskGraph) CheckExecutable(id, repo string) error {
	if err := g.Validate(); err != nil {
		return err
	}
	t, ok := g.task(id)
	if !ok {
		return graphError("Task lost: untracked task %s", id)
	}
	if t.Repo != repo || t.Status != Ready {
		return graphError("task %s requires matching repository and READY", id)
	}
	return g.executable(t)
}

func cloneGraph(g TaskGraph) TaskGraph {
	copy := g
	copy.Tasks = slices.Clone(g.Tasks)
	for i := range copy.Tasks {
		copy.Tasks[i] = cloneTask(copy.Tasks[i])
	}
	copy.Concerns = slices.Clone(g.Concerns)
	copy.History = slices.Clone(g.History)
	for i := range copy.History {
		copy.History[i].Task = cloneTask(copy.History[i].Task)
		if copy.History[i].Proof != nil {
			proof := *copy.History[i].Proof
			proof.Evidence = slices.Clone(proof.Evidence)
			proof.Acceptance = slices.Clone(proof.Acceptance)
			proof.Waivers = cloneWaivers(proof.Waivers)
			copy.History[i].Proof = &proof
		}
	}
	return copy
}

func cloneWaivers(waivers []TaskWaiver) []TaskWaiver {
	copy := slices.Clone(waivers)
	for i := range copy {
		copy[i].Acceptance = slices.Clone(copy[i].Acceptance)
		copy[i].Evidence = slices.Clone(copy[i].Evidence)
		copy[i].Children = slices.Clone(copy[i].Children)
	}
	return copy
}

func cloneTask(t GraphTask) GraphTask {
	t.DependsOn = slices.Clone(t.DependsOn)
	t.Scope = slices.Clone(t.Scope)
	t.OutOfScope = slices.Clone(t.OutOfScope)
	t.Acceptance = slices.Clone(t.Acceptance)
	t.RequiredEvidence = slices.Clone(t.RequiredEvidence)
	t.ExceptionIDs = slices.Clone(t.ExceptionIDs)
	t.Decisions = slices.Clone(t.Decisions)
	t.MemoryRefs = slices.Clone(t.MemoryRefs)
	return t
}

func (g *TaskGraph) Track(t GraphTask, actor, reason string) error {
	next := cloneGraph(*g)
	t = cloneTask(t)
	if old, ok := next.task(t.ID); ok {
		if terminal(old.Status) || old.Status != t.Status || old.Repo != t.Repo || old.CreatedFrom != t.CreatedFrom ||
			!slices.Contains([]TaskStatus{Draft, Blocked, HumanDecisionRequired}, old.Status) {
			return graphError("task identity/status or running/terminal contract cannot be edited")
		}
		for _, d := range old.Decisions {
			if !slices.Contains(t.Decisions, d) {
				return graphError("decision %s cannot be removed or rewritten", d.ID)
			}
		}
		for i := range next.Tasks {
			if next.Tasks[i].ID == t.ID {
				next.Tasks[i] = t
			}
		}
	} else {
		if t.Status != Draft && t.Status != Blocked {
			return graphError("new task must be DRAFT or BLOCKED")
		}
		next.Tasks = append(next.Tasks, t)
	}
	next.History = append(next.History, GraphEvent{TaskID: t.ID, Actor: actor, Reason: reason, At: time.Now().UTC(), Task: t})
	if err := next.Validate(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g *TaskGraph) AddConcern(c Concern) error {
	next := cloneGraph(*g)
	next.Concerns = append(next.Concerns, c)
	if err := next.Validate(); err != nil {
		return err
	}
	*g = next
	return nil
}

func allowedTransition(from, to TaskStatus) bool {
	if terminal(from) || from == to {
		return false
	}
	if to == Blocked || to == HumanDecisionRequired || to == Cancelled || to == Rejected || to == Superseded {
		return true
	}
	switch from {
	case Draft:
		return to == Ready
	case Blocked, HumanDecisionRequired:
		return to == Draft || to == Ready
	case Ready:
		return to == Active || to == Draft
	case Active:
		return to == WaitingForEvidence || to == WaitingForReview
	case WaitingForEvidence:
		return to == WaitingForReview
	case WaitingForReview:
		return to == Approved || to == RequestChanges
	case RequestChanges:
		return to == Ready
	case Approved:
		return to == Completed
	}
	return false
}

func (g *TaskGraph) Transition(id string, status TaskStatus, actor, reason string, proof TransitionProof) error {
	next := cloneGraph(*g)
	proof.Evidence = slices.Clone(proof.Evidence)
	proof.Acceptance = slices.Clone(proof.Acceptance)
	proof.Waivers = cloneWaivers(proof.Waivers)
	t, ok := next.task(id)
	if !ok || !allowedTransition(t.Status, status) {
		return graphError("invalid transition %s -> %s", t.Status, status)
	}
	if t.DeferredReason != "" && status != Blocked && status != Cancelled && status != Rejected && status != Superseded {
		return graphError("clear deferral with a recorded contract update before resuming")
	}
	if proof.OutputSHA != "" {
		if status != WaitingForEvidence && status != WaitingForReview {
			return graphError("output SHA is only accepted at evidence handoff")
		}
		t.TargetSHA = proof.OutputSHA
	}
	if status == Approved || status == Completed {
		if err := completionProof(t, proof); err != nil {
			return err
		}
		for _, child := range next.Tasks {
			if child.ParentID == id && child.Status != Completed && !waived(proof, child.ID, "child") {
				return graphError("child %s is not completed and has no approved waiver", child.ID)
			}
		}
	}
	if status == Superseded {
		found := false
		for _, replacement := range next.Tasks {
			if replacement.Supersedes == id {
				found = true
			}
		}
		if !found {
			return graphError("supersession requires a replacement task")
		}
	}
	t.Status = status
	for i := range next.Tasks {
		if next.Tasks[i].ID == id {
			next.Tasks[i] = t
		}
	}
	next.History = append(next.History, GraphEvent{TaskID: id, Actor: actor, Reason: reason, At: time.Now().UTC(), Task: t, Proof: &proof})
	for i, candidate := range next.Tasks {
		if candidate.ID != id && slices.Contains([]TaskStatus{Ready, Active, WaitingForEvidence, WaitingForReview, RequestChanges, Approved}, candidate.Status) {
			if err := next.executable(candidate); err != nil {
				candidate.Status = Blocked
				candidate.NextAction = err.Error()
				next.Tasks[i] = candidate
				next.History = append(next.History, GraphEvent{TaskID: candidate.ID, Actor: actor, Reason: "dependency reevaluation: " + err.Error(), At: time.Now().UTC(), Task: candidate})
			}
		}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*g = next
	return nil
}

func completionProof(t GraphTask, proof TransitionProof) error {
	if !proof.ActionPerformed || len(t.RequiredEvidence) == 0 || len(t.Acceptance) == 0 {
		return graphError("completion requires performed action and evidence")
	}
	for _, waiver := range proof.Waivers {
		if !slices.Contains(t.ExceptionIDs, waiver.DecisionID) {
			return graphError("waiver must reference a recorded exception")
		}
		found := false
		for _, decision := range t.Decisions {
			if decision.ID == waiver.DecisionID {
				found = true
			}
		}
		if !found || !validList(waiver.Acceptance) || !validList(waiver.Evidence) || !validList(waiver.Children) {
			return graphError("invalid or unapproved waiver")
		}
	}
	for _, required := range t.RequiredEvidence {
		if !slices.Contains(proof.Evidence, required) && !waived(proof, required, "evidence") {
			return graphError("missing evidence %s", required)
		}
	}
	for _, criterion := range t.Acceptance {
		if !slices.Contains(proof.Acceptance, criterion) && !waived(proof, criterion, "acceptance") {
			return graphError("unmet acceptance %s", criterion)
		}
	}
	if t.Reviewer == "" || proof.Reviewer != t.Reviewer || t.Reviewer == t.Owner ||
		t.TargetSHA == "" || proof.ReviewedSHA != t.TargetSHA {
		return graphError("independent review of the exact target SHA required")
	}
	return nil
}

func waived(proof TransitionProof, value, kind string) bool {
	for _, waiver := range proof.Waivers {
		switch kind {
		case "acceptance":
			if slices.Contains(waiver.Acceptance, value) {
				return true
			}
		case "evidence":
			if slices.Contains(waiver.Evidence, value) {
				return true
			}
		case "child":
			if slices.Contains(waiver.Children, value) {
				return true
			}
		}
	}
	return false
}

// GraphPath is tied to the selected ledger, including custom ledger filenames.
func GraphPath(ledgerPath string) string { return ledgerPath + ".graph.json" }

func DecodeGraphInput(r io.Reader, v any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return graphError("multiple JSON values")
		}
		return err
	}
	return nil
}

func LoadTaskGraph(path string) (TaskGraph, error) {
	f, err := os.Open(path)
	if err != nil {
		return TaskGraph{}, err
	}
	defer f.Close()
	var g TaskGraph
	if err := DecodeGraphInput(f, &g); err != nil {
		return TaskGraph{}, err
	}
	if err := g.Validate(); err != nil {
		return TaskGraph{}, err
	}
	return g, nil
}

func (g TaskGraph) Save(path string) error {
	if err := g.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".task-graph-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func CheckGraphDispatch(ledgerPath string, t Task, repo string) error {
	g, err := LoadTaskGraph(GraphPath(ledgerPath))
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(GraphPath(ledgerPath)); errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("task graph exists but cannot be loaded: %w", err)
	}
	if err != nil {
		return fmt.Errorf("load task graph: %w", err)
	}
	ledger, err := LoadLedger(ledgerPath)
	if err != nil {
		return fmt.Errorf("validate graph ledger: %w", err)
	}
	if err := g.CheckLedger(ledger); err != nil {
		return err
	}
	tracked, ok := g.task(t.ID)
	if !ok || tracked.Title != t.Title || tracked.Repo != t.Repo {
		return graphError("Task lost or ledger identity mismatch for %s", t.ID)
	}
	return g.CheckExecutable(t.ID, repo)
}

func (g TaskGraph) CheckLedger(ledger Ledger) error {
	for _, tracked := range g.Tasks {
		found := false
		for _, task := range ledger.Tasks {
			if task.ID == tracked.ID {
				found = true
				if task.Title != tracked.Title || task.Repo != tracked.Repo {
					return graphError("ledger identity mismatch for %s", task.ID)
				}
			}
		}
		if !found {
			return graphError("Task lost: tracked task %s missing from ledger", tracked.ID)
		}
	}
	return nil
}
