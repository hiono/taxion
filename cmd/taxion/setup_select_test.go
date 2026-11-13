package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSetupSelectionKeysAndSafeDefault(t *testing.T) {
	for _, test := range []struct {
		name, keys, want string
	}{
		{"default-cancel", "\r", "Cancel"},
		{"down-apply", "\x1b[B\r", "Apply"},
		{"up-wrap", "\x1b[A\n", "Apply"},
		{"return-to-cancel", "\x1b[B\x1b[A\r", "Cancel"},
		{"application-keys", "\x1bOB\r", "Apply"},
		{"ime-input-ignored", "ｙｅｓ\r", "Cancel"},
		{"text-not-confirmation", "yes\r", "Cancel"},
		{"ctrl-c", "\x03", ""},
		{"ctrl-d", "\x04", ""},
		{"eof", "", ""},
		{"incomplete-escape", "\x1b[", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := readSetupSelection(bufio.NewReader(strings.NewReader(test.keys)), &output, "Apply?", []string{"Cancel", "Apply"}, 0)
			if got != test.want || (err != nil) != (test.want == "") {
				t.Fatal(got, err, output.String())
			}
			if !strings.Contains(output.String(), "Cancel / Apply") {
				t.Fatal("selection omitted available choices")
			}
			if err != nil && !strings.Contains(err.Error(), "no files changed") {
				t.Fatal("missing cancellation boundary", err)
			}
		})
	}
	t.Run("suggested-provider", func(t *testing.T) {
		got, err := readSetupSelection(bufio.NewReader(strings.NewReader("\r")), io.Discard, "runtime.provider", []string{"copilot", "opencode"}, 1)
		if err != nil || got != "opencode" {
			t.Fatal(got, err)
		}
	})
	t.Run("output-failure", func(t *testing.T) {
		_, err := readSetupSelection(bufio.NewReader(strings.NewReader("\r")), rejectingConfigWriter{}, "Apply?", []string{"Cancel", "Apply"}, 0)
		if err == nil || !strings.Contains(err.Error(), "output unavailable") {
			t.Fatal("output failure was suppressed", err)
		}
	})
	t.Run("input-failure", func(t *testing.T) {
		_, err := readSetupSelection(bufio.NewReader(strings.NewReader("")), io.Discard, "Apply?", []string{"Cancel", "Apply"}, 0)
		if !errors.Is(err, io.EOF) {
			t.Fatal("input failure was suppressed", err)
		}
	})
}
