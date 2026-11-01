package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// request suppresses worker transcripts, but must not hide a pre-stop decision.
type resourceApprovalOutput struct {
	output  io.Writer
	pending []byte
}

func (w *resourceApprovalOutput) Write(data []byte) (int, error) {
	w.pending = append(w.pending, data...)
	for {
		end := bytes.IndexByte(w.pending, '\n')
		if end < 0 {
			break
		}
		line := w.pending[:end]
		w.pending = w.pending[end+1:]
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &event) == nil && event.Type == "resource_approval_required" {
			if _, err := fmt.Fprintln(w.output, string(line)); err != nil {
				return 0, err
			}
		}
	}
	return len(data), nil
}
