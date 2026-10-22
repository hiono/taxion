package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func rolePacketListCommand(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("role-packet list", flag.ContinueOnError)
	var diagnostics bytes.Buffer
	fs.SetOutput(&diagnostics)
	task := fs.String("task", "", "original Task ID")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := io.WriteString(output, diagnostics.String())
			return err
		}
		return fmt.Errorf("taxion: invalid role-packet list options")
	}
	if fs.NArg() != 0 || *task == "" {
		return fmt.Errorf("usage: taxion role-packet list --task TASK_ID")
	}
	state, err := packetWorkspaceSnapshot()
	if err != nil {
		return authorizeError(err)
	}
	if _, ok := state.Graph.GraphTask(*task); !ok {
		return fmt.Errorf("packet Task not found")
	}
	result := struct {
		TaskID         string                      `json:"taskId"`
		Classification string                      `json:"classification"`
		Authorizations []store.PacketAuthorization `json:"authorizations"`
		Audit          []store.PacketAudit         `json:"audit"`
	}{TaskID: *task, Classification: "legacy-unverified",
		Authorizations: []store.PacketAuthorization{}, Audit: []store.PacketAudit{}}
	for _, authorization := range state.Records.PacketAuthorizations {
		if authorization.TaskID == *task {
			result.Authorizations = append(result.Authorizations, authorization)
			result.Classification = "lifecycle-authorized"
		}
	}
	for _, audit := range state.Records.PacketAudits {
		if audit.TaskID == *task {
			result.Audit = append(result.Audit, audit)
		}
	}
	return json.NewEncoder(output).Encode(result)
}

type packetLifecycleRequest struct {
	Version     int                       `json:"version"`
	TaskID      string                    `json:"taskId"`
	Actor       string                    `json:"actor"`
	Source      string                    `json:"source"`
	Reason      string                    `json:"reason"`
	Unavailable []store.UnavailablePacket `json:"unavailable"`
}

func packetWorkspaceSnapshot() (store.Workspace, error) {
	if err := validateWorkspaceStorePath(); err != nil {
		return store.Workspace{}, err
	}
	db, done, err := openWorkspaceSnapshot(workspaceDBPath())
	if err != nil {
		return store.Workspace{}, err
	}
	defer done()
	return db.LoadWorkspace()
}

func taskAuthorizePacketsCmd(args []string) error {
	fs := flag.NewFlagSet("task authorize-packets", flag.ContinueOnError)
	var diagnostics bytes.Buffer
	fs.SetOutput(&diagnostics)
	file := fs.String("request", "", "versioned packet lifecycle authorization request")
	confirm := fs.String("confirm", "", "exact confirmation token from preview")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := io.WriteString(os.Stdout, diagnostics.String())
			return err
		}
		return fmt.Errorf("taxion: invalid task authorize-packets options")
	}
	if fs.NArg() != 0 || *file == "" {
		return fmt.Errorf("usage: taxion task authorize-packets --request FILE [--confirm TOKEN]")
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return authorizeError(err)
	}
	var request packetLifecycleRequest
	if err := core.DecodeGraphInput(strings.NewReader(string(data)), &request); err != nil {
		return authorizeError(err)
	}
	if request.Version != 1 {
		return fmt.Errorf("packet lifecycle request version is not supported")
	}
	state, err := packetWorkspaceSnapshot()
	if err != nil {
		return authorizeError(err)
	}
	configuration, err := approvalConfiguration()
	if err != nil {
		return authorizeError(err)
	}
	preview, err := controller.PreviewPacketAuthorization(state, request.TaskID,
		request.Actor, request.Source, request.Reason, request.Unavailable, configuration)
	if err != nil {
		return authorizeError(err)
	}
	token := "sha256:" + preview.Hash
	if *confirm == "" {
		return printJSON(struct {
			Authorization     store.PacketAuthorization `json:"authorization"`
			ConfirmationToken string                    `json:"confirmationToken"`
		}{preview, token})
	}
	if subtle.ConstantTimeCompare([]byte(*confirm), []byte(token)) != 1 {
		return fmt.Errorf("packet lifecycle confirmation does not match the preview basis")
	}
	if err := updateWorkspace(func(current *store.Workspace) error {
		fresh, err := approvalConfiguration()
		if err != nil {
			return err
		}
		return controller.AppendPacketAuthorization(current, preview, fresh)
	}); err != nil {
		return authorizeError(err)
	}
	saved, err := packetWorkspaceSnapshot()
	if err != nil {
		return authorizeError(err)
	}
	authorization, ok := saved.Records.PacketAuthorization(preview.TaskID)
	if !ok || authorization.Hash != preview.Hash {
		return fmt.Errorf("packet lifecycle authorization readback mismatch")
	}
	if err := authorization.Verify(); err != nil {
		return authorizeError(err)
	}
	return printJSON(authorization)
}

func rolePacketReadCommand(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("role-packet read", flag.ContinueOnError)
	var diagnostics bytes.Buffer
	fs.SetOutput(&diagnostics)
	hash := fs.String("expected-hash", "", "independently pinned stored packet hash")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := io.WriteString(output, diagnostics.String())
			return err
		}
		return fmt.Errorf("taxion: invalid role-packet read options")
	}
	if fs.NArg() != 0 || *hash == "" {
		return fmt.Errorf("usage: taxion role-packet read --expected-hash HASH")
	}
	state, err := packetWorkspaceSnapshot()
	if err != nil {
		return authorizeError(err)
	}
	packet, err := state.Records.ReadPacket(*hash)
	if err != nil {
		return authorizeError(err)
	}
	data, err := packet.MarshalJSON()
	if err != nil {
		return authorizeError(err)
	}
	_, err = fmt.Fprintln(output, string(data))
	return err
}
