package main

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestResourceApprovalOutputPreservesSplitDecisionAndHidesTranscript(t *testing.T) {
	var output bytes.Buffer
	writer := &resourceApprovalOutput{output: &output}
	for _, part := range []string{
		"{\"type\":\"assistant.message\",\"text\":\"private transcript\"}\n{\"type\":\"resource_",
		"approval_required\",\"flowId\":\"flow\"}\n{\"type\":\"usage\"}\n",
	} {
		if n, err := io.WriteString(writer, part); err != nil || n != len(part) {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if got, want := output.String(), "{\"type\":\"resource_approval_required\",\"flowId\":\"flow\"}\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	writer.output = failingResourceOutput{}
	if _, err := io.WriteString(writer, "{\"type\":\"resource_approval_required\"}\n"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost output error: %v", err)
	}
}

type failingResourceOutput struct{}

func (failingResourceOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
