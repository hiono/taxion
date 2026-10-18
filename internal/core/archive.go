package core

import (
	"slices"
	"time"
)

// TaskArchive retires work without rewriting its contracts or execution history.
type TaskArchive struct {
	TaskIDs      []string  `json:"taskIds"`
	Actor        string    `json:"actor"`
	Source       string    `json:"source"`
	Reason       string    `json:"reason"`
	ExpectedHash string    `json:"expectedHash"`
	At           time.Time `json:"at"`
}

func (g TaskGraph) IsArchived(taskID string) bool {
	for _, archive := range g.Archives {
		if slices.Contains(archive.TaskIDs, taskID) {
			return true
		}
	}
	return false
}

// ArchiveScope includes each selected flow's issued role Tasks, not arbitrary
// dependent work. Those dependencies must be reviewed and selected explicitly.
func (g TaskGraph) ArchiveScope(taskIDs []string) ([]string, error) {
	if len(taskIDs) == 0 || !validList(taskIDs) {
		return nil, graphError("archive requires distinct Task IDs")
	}
	selected := slices.Clone(taskIDs)
	for index := 0; index < len(selected); index++ {
		id := selected[index]
		if _, ok := g.task(id); !ok || g.IsArchived(id) {
			return nil, graphError("archive requires an existing unarchived Task: %s", id)
		}
		for _, flow := range g.ReviewFlows {
			ownsTask := flow.TaskID == id
			for _, dispatchID := range flow.DispatchIDs {
				dispatch, _ := g.dispatch(dispatchID)
				ownsTask = ownsTask || dispatch.TaskID == id
			}
			if !ownsTask {
				continue
			}
			if !slices.Contains(selected, flow.TaskID) {
				selected = append(selected, flow.TaskID)
			}
			for _, dispatchID := range flow.DispatchIDs {
				dispatch, ok := g.dispatch(dispatchID)
				if !ok {
					return nil, graphError("archive flow %s has a missing Dispatch", flow.ID)
				}
				if !slices.Contains(selected, dispatch.TaskID) {
					selected = append(selected, dispatch.TaskID)
				}
			}
		}
	}
	slices.Sort(selected)
	if err := g.checkArchiveQuiescent(selected, true); err != nil {
		return nil, err
	}
	return selected, nil
}

func (g TaskGraph) checkArchiveQuiescent(selected []string, checkReferences bool) error {
	inScope := func(id string) bool { return slices.Contains(selected, id) }
	for _, dispatch := range g.Dispatches {
		if inScope(dispatch.TaskID) && dispatchActive(dispatch.Status) {
			return graphError("archive requires terminal Dispatch %s (has %s)", dispatch.ID, dispatch.Status)
		}
	}
	for _, flow := range g.ReviewFlows {
		if inScope(flow.TaskID) && (flow.Status == "RUNNING" || flow.RunnerID != "") {
			return graphError("archive requires a stopped, unclaimed flow: %s", flow.ID)
		}
	}
	for _, task := range g.Tasks {
		if inScope(task.ID) {
			if task.Status == Active && !slices.ContainsFunc(g.ReviewFlows, func(flow ReviewFlow) bool {
				return flow.TaskID == task.ID && flow.Status == "STOPPED"
			}) {
				return graphError("archive requires reconciling ACTIVE Task %s", task.ID)
			}
			continue
		}
		if terminal(task.Status) || g.IsArchived(task.ID) {
			continue
		}
		if !checkReferences {
			continue
		}
		referenced := inScope(task.ParentID) || inScope(task.Supersedes) ||
			slices.ContainsFunc(task.DependsOn, func(dep TaskDependency) bool { return inScope(dep.TaskID) })
		for _, child := range g.Tasks {
			if inScope(child.ID) && child.ParentID == task.ID {
				referenced = true
			}
		}
		for _, dispatch := range g.Dispatches {
			if inScope(dispatch.TaskID) && task.CreatedFrom == "dispatch:"+dispatch.ID {
				referenced = true
			}
			if dispatch.TaskID == task.ID && dispatch.RelatedID != "" {
				related, _ := g.dispatch(dispatch.RelatedID)
				referenced = referenced || inScope(related.TaskID)
			}
		}
		for _, flow := range g.ReviewFlows {
			if inScope(flow.TaskID) && task.CreatedFrom == "flow:"+flow.ID {
				referenced = true
			}
		}
		if referenced {
			return graphError("unarchived nonterminal Task %s references the archive scope; review it before including it", task.ID)
		}
	}
	return nil
}

func (g TaskGraph) validateArchives() error {
	seen := map[string]bool{}
	for _, archive := range g.Archives {
		if len(archive.TaskIDs) == 0 || !validList(archive.TaskIDs) || !nonempty(archive.Actor) ||
			!nonempty(archive.Source) || !nonempty(archive.Reason) || archive.At.IsZero() ||
			!validDocumentHash(archive.ExpectedHash) {
			return graphError("invalid Task archive receipt")
		}
		for _, id := range archive.TaskIDs {
			if _, ok := g.task(id); !ok || seen[id] {
				return graphError("archive contains missing or repeated Task %s", id)
			}
			seen[id] = true
		}
		if err := g.checkArchiveQuiescent(archive.TaskIDs, false); err != nil {
			return err
		}
	}
	return nil
}
