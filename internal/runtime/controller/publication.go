package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitguard"
	"github.com/hiono/taxion/internal/store"
)

type PublicationRequest struct {
	Update       store.ExternalUpdate `json:"update"`
	ExpectedHash string               `json:"expectedHash"`
}

type ExternalPublisher interface {
	Check(context.Context, store.ExternalUpdate) error
	Reconcile(context.Context, store.ExternalUpdate) (string, bool, error)
	Send(context.Context, store.ExternalUpdate) (string, error)
}

var externalID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)
var repositoryPath = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var publicationCredential = regexp.MustCompile(`^env:[A-Z_][A-Z0-9_]*$`)

func PublicationHash(update store.ExternalUpdate) (string, error) {
	if !externalID.MatchString(update.ID) || !repositoryPath.MatchString(update.Repository) ||
		update.Provider != "github" || update.Host != "github.com" ||
		update.TaskID == "" || update.Account == "" || update.Actor == "" || update.ApprovalSource == "" ||
		update.Kind != "ISSUE" && update.Kind != "PR" {
		return "", fmt.Errorf("taxion: publication requires exact identity, target, account and sourced Human approval")
	}
	if update.Status != "" || update.Hash != "" || update.Claim != "" || update.RemoteID != "" || update.Error != "" || !update.At.IsZero() {
		return "", fmt.Errorf("taxion: proposal must contain only requested publication fields")
	}
	if update.CredentialRef != "gh:active" && !publicationCredential.MatchString(update.CredentialRef) {
		return "", fmt.Errorf("taxion: publication requires gh:active or env:NAME")
	}
	switch update.Action {
	case "CREATE":
		if update.Kind != "ISSUE" || update.Number != 0 || strings.TrimSpace(update.Title) == "" || strings.TrimSpace(update.Body) == "" {
			return "", fmt.Errorf("taxion: Issue creation requires title and body")
		}
	case "COMMENT":
		if update.Number <= 0 || strings.TrimSpace(update.Body) == "" || update.Title != "" {
			return "", fmt.Errorf("taxion: comment requires an existing object and body")
		}
	case "CLOSE":
		if update.Number <= 0 || update.Kind != "ISSUE" || update.FlowID == "" || update.Body != "" || update.Title != "" {
			return "", fmt.Errorf("taxion: close requires an Issue and verified completed flow")
		}
	default:
		return "", fmt.Errorf("taxion: unsupported external update action")
	}
	if len(update.Body) > 60000 || len(update.Title) > 256 {
		return "", fmt.Errorf("taxion: publication exceeds the approved text limit")
	}
	body, err := json.Marshal(update)
	return receiptHash(string(body)), err
}

func ApprovePublication(ctx context.Context, db *store.Store, request PublicationRequest, scanner string) (store.ExternalUpdate, error) {
	hash, err := PublicationHash(request.Update)
	if err != nil {
		return store.ExternalUpdate{}, err
	}
	if request.ExpectedHash != hash {
		return store.ExternalUpdate{}, fmt.Errorf("taxion: publication approval hash differs")
	}
	if err := gitguard.CheckPublication(ctx, scanner, request.Update.Title+"\n"+request.Update.Body); err != nil {
		return store.ExternalUpdate{}, err
	}
	update := request.Update
	update.Hash, update.Status, update.At = hash, "PENDING", time.Now().UTC()
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		for _, saved := range state.Records.Updates {
			if saved.ID == update.ID {
				if saved.Hash == hash {
					update = saved
					return nil
				}
				return fmt.Errorf("taxion: publication ID already has a different approval")
			}
		}
		if err := checkPublicationTask(state, update); err != nil {
			return err
		}
		state.Records.Updates = append(state.Records.Updates, update)
		return nil
	})
	return update, err
}

func checkPublicationTask(state *store.Workspace, update store.ExternalUpdate) error {
	proposal := update
	proposal.Hash, proposal.Status, proposal.Claim, proposal.RemoteID, proposal.Error = "", "", "", "", ""
	proposal.At = time.Time{}
	hash, err := PublicationHash(proposal)
	if err != nil || hash != update.Hash {
		return fmt.Errorf("taxion: approved publication content changed")
	}
	task, ok := state.Graph.GraphTask(update.TaskID)
	taskRepository := update.TaskRepository
	if taskRepository == "" {
		taskRepository = update.Repository
	}
	if !state.HasGraph || !ok || task.Repo != taskRepository {
		return fmt.Errorf("taxion: publication Task or repository differs")
	}
	if err := state.Graph.CheckTaskSpecificationUse(task.ID, "publication"); err != nil {
		return err
	}
	if update.Kind == "ISSUE" && update.Action != "CREATE" && task.Issue != fmt.Sprintf("Issue #%d", update.Number) {
		return fmt.Errorf("taxion: publication Issue differs from registered Task")
	}
	if update.Action == "CLOSE" {
		flow, ok := state.Graph.ReviewFlow(update.FlowID)
		if !ok || flow.TaskID != task.ID || flow.Status != "COMPLETED" || task.Status != core.Completed ||
			task.Issue != fmt.Sprintf("Issue #%d", update.Number) {
			return fmt.Errorf("taxion: close requires matched completed Task, Issue and flow")
		}
		for _, id := range flow.DispatchIDs {
			d, _ := state.Graph.GraphDispatch(id)
			if err := validateResultReceipt(state, d); err != nil {
				return err
			}
		}
		if err := revalidateFlowResult(state.Graph, flow); err != nil {
			return err
		}
		for _, dependency := range task.DependsOn {
			current, ok := state.Graph.GraphTask(dependency.TaskID)
			if !ok || current.Status != dependency.State && !(dependency.State == core.Approved && current.Status == core.Completed) {
				return fmt.Errorf("taxion: dependency state changed")
			}
		}
		for _, exception := range task.ExceptionIDs {
			approved := false
			for _, decision := range task.Decisions {
				if decision.ID == exception && strings.TrimSpace(decision.Source) != "" && strings.TrimSpace(decision.Text) != "" {
					approved = true
				}
			}
			if !approved {
				return fmt.Errorf("taxion: close requires a sourced decision for exception %s", exception)
			}
		}
		for _, pending := range state.Records.Updates {
			if pending.TaskID == task.ID && pending.ID != update.ID && pending.Status != "SENT" {
				return fmt.Errorf("taxion: close requires synchronized preceding external records")
			}
		}
	}
	if update.Action == "CREATE" {
		if task.Status != core.Draft || !strings.HasPrefix(task.Issue, "PENDING:") {
			return fmt.Errorf("taxion: create-and-link requires a DRAFT Task")
		}
		for _, pending := range state.Records.Updates {
			if pending.TaskID == task.ID && pending.Action == "CREATE" && pending.ID != update.ID {
				return fmt.Errorf("taxion: Task already has an approved Issue registration")
			}
		}
		for _, dispatch := range state.Graph.Dispatches {
			if dispatch.TaskID == task.ID {
				return fmt.Errorf("taxion: Issue registration precedes Task execution")
			}
		}
	}
	return nil
}

func DeliverPublication(ctx context.Context, db *store.Store, id, scanner string, publisher ExternalPublisher) (store.ExternalUpdate, error) {
	if publisher == nil {
		return store.ExternalUpdate{}, fmt.Errorf("taxion: publication requires an adapter")
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return store.ExternalUpdate{}, err
	}
	claim := hex.EncodeToString(token)
	var update store.ExternalUpdate
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		for i := range state.Records.Updates {
			saved := &state.Records.Updates[i]
			if saved.ID != id {
				continue
			}
			update = *saved
			if saved.Status == "SENT" {
				return nil
			}
			if saved.Status != "PENDING" {
				return fmt.Errorf("taxion: publication requires explicit reconciliation of claim %s", saved.Claim)
			}
			if err := checkPublicationTask(state, *saved); err != nil {
				return err
			}
			saved.Claim, saved.Status = claim, "CLAIMED"
			update = *saved
			return nil
		}
		return fmt.Errorf("taxion: approved publication not found")
	})
	if err != nil || update.Status == "SENT" {
		return update, err
	}
	remoteID, delivered := "", false
	sendErr := gitguard.CheckPublication(ctx, scanner, update.Title+"\n"+update.Body)
	if sendErr == nil {
		sendErr = publisher.Check(ctx, update)
	}
	if sendErr == nil {
		remoteID, delivered, sendErr = publisher.Reconcile(ctx, update)
	}
	attempted := false
	if sendErr == nil && !delivered {
		sendErr = db.UpdateWorkspace(func(state *store.Workspace) error {
			for _, saved := range state.Records.Updates {
				if saved.ID == id && saved.Claim == claim && saved.Status == "CLAIMED" && saved.Hash == update.Hash {
					return checkPublicationTask(state, saved)
				}
			}
			return fmt.Errorf("taxion: publication claim changed before remote write")
		})
	}
	if sendErr == nil && !delivered {
		attempted = true
		remoteID, sendErr = publisher.Send(ctx, update)
		delivered = sendErr == nil
	}
	saveErr := db.UpdateWorkspace(func(state *store.Workspace) error {
		for i := range state.Records.Updates {
			saved := &state.Records.Updates[i]
			if saved.ID != id {
				continue
			}
			if saved.Claim != claim || saved.Status != "CLAIMED" || saved.Hash != update.Hash {
				return fmt.Errorf("taxion: publication claim changed during delivery")
			}
			if sendErr != nil {
				saved.Status = "PENDING"
				if attempted {
					saved.Status = "UNKNOWN"
				} else {
					saved.Claim = ""
				}
				saved.Error = "publication failed; inspect permissions, scanner or remote delivery and reconcile"
				update = *saved
				return nil
			}
			if !delivered || remoteID == "" {
				return fmt.Errorf("taxion: adapter returned incomplete delivery evidence")
			}
			saved.Status, saved.RemoteID, saved.Error = "SENT", remoteID, ""
			if update.Action == "CREATE" {
				if err := checkPublicationTask(state, update); err != nil {
					return err
				}
				task, _ := state.Graph.GraphTask(update.TaskID)
				task.Issue = "Issue #" + remoteID
				if err := state.Graph.Track(task, update.Actor, "approved external Issue registration"); err != nil {
					return err
				}
			}
			update = *saved
			return nil
		}
		return fmt.Errorf("taxion: publication disappeared during delivery")
	})
	return update, errors.Join(sendErr, saveErr)
}

func ReconcilePublication(ctx context.Context, db *store.Store, id, expectedClaim string, processStopped bool, publisher ExternalPublisher) (store.ExternalUpdate, error) {
	if !processStopped || expectedClaim == "" || publisher == nil {
		return store.ExternalUpdate{}, fmt.Errorf("taxion: reconciliation requires exact stopped sender claim")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return store.ExternalUpdate{}, err
	}
	var update store.ExternalUpdate
	for _, saved := range state.Records.Updates {
		if saved.ID == id {
			update = saved
		}
	}
	if update.Claim != expectedClaim || update.Status != "UNKNOWN" && update.Status != "CLAIMED" {
		return update, fmt.Errorf("taxion: uncertain publication claim differs")
	}
	if err := publisher.Check(ctx, update); err != nil {
		return update, err
	}
	remoteID, found, err := publisher.Reconcile(ctx, update)
	if err != nil {
		return update, err
	}
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		for i := range state.Records.Updates {
			saved := &state.Records.Updates[i]
			if saved.ID != id {
				continue
			}
			if !reflect.DeepEqual(*saved, update) {
				return fmt.Errorf("taxion: publication changed during reconciliation")
			}
			if found {
				if remoteID == "" {
					return fmt.Errorf("taxion: reconciliation requires remote identity")
				}
				saved.Status, saved.RemoteID = "SENT", remoteID
				if saved.Action == "CREATE" {
					if err := checkPublicationTask(state, update); err != nil {
						return err
					}
					task, _ := state.Graph.GraphTask(saved.TaskID)
					task.Issue = "Issue #" + remoteID
					if err := state.Graph.Track(task, saved.Actor, "reconciled approved Issue registration"); err != nil {
						return err
					}
					saved.Error = ""
				}
			} else {
				// An absent marker cannot exclude an in-flight provider request.
				return fmt.Errorf("taxion: remote delivery remains unconfirmed; Human must inspect provider state before new authorization")
			}
			update = *saved
			return nil
		}
		return fmt.Errorf("taxion: publication disappeared")
	})
	return update, err
}
