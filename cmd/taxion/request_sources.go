package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func requestContextSources(state store.Workspace, id string) ([]core.ContextDocument, error) {
	var documents []core.ContextDocument
	for _, record := range state.Records.Requests {
		if record.RequestID != id || record.Kind != "context-source" {
			continue
		}
		var document core.ContextDocument
		if err := core.DecodeGraphInput(strings.NewReader(record.Payload), &document); err != nil {
			return nil, fmt.Errorf("request source snapshot is invalid")
		}
		documents = append(documents, document)
	}
	return documents, core.CheckContextDocuments(documents)
}

func captureRequestSource(db *store.Store, id, kind, source, text string, alreadyRedacted ...bool) error {
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	documents, err := requestContextSources(state, id)
	if err != nil {
		return err
	}
	version := "sha256:" + core.SHA256Hex([]byte(text))
	redacted := core.Redact(text)
	if redacted != text || len(alreadyRedacted) != 0 && alreadyRedacted[0] {
		version += ":redacted"
	}
	document, err := core.CaptureContextDocument(core.ContextDocument{
		ID:   fmt.Sprintf("request/%s/source/%d", id, len(documents)+1),
		Kind: kind, Source: source, Version: version, Text: redacted,
	})
	if err != nil {
		return err
	}
	body, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return db.AppendRequestRecord(id, "context-source", string(body))
}
