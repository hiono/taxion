package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicTextCLI(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "report.json")
	valid := `{"outcome":"REQUEST_CHANGES","commit":"` + strings.Repeat("a", 40) +
		`","feedbackAddressed":["Initial review."],"changes":["No changes."],"validation":["Not run."],"remainingConcerns":["Missing exact-commit evidence."]}`
	for _, input := range []string{valid, valid + " {}", strings.Replace(valid, `"outcome"`, `"unknown"`, 1), strings.Repeat(" ", 65537)} {
		if err := os.WriteFile(file, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}

		var output, warnings bytes.Buffer
		err := run([]string{"-report", file}, &output, &warnings)
		if input == valid {
			if err != nil || !strings.HasPrefix(output.String(), "Changes requested\n") || !strings.Contains(output.String(), "Not run.") {
				t.Fatal("valid report not rendered accurately", err)
			}
		} else if err == nil || output.Len() != 0 {
			t.Fatal("invalid report produced public output")
		}
	}
	text := filepath.Join(directory, "message.md")
	if err := os.WriteFile(text, []byte("Private-Example-47"), 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("private-example-47"))
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", hex.EncodeToString(sum[:]))
	var output, warnings bytes.Buffer
	if run([]string{text}, &output, &warnings) == nil || output.Len() != 0 {
		t.Fatal("configured protected identifier scan bypassed")
	}
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", "")
	if err := run([]string{text}, &output, &warnings); err != nil || !strings.Contains(warnings.String(), "unavailable") {
		t.Fatal("missing private identifier coverage not disclosed", err)
	}
}

func TestDocumentModeCannotOverrideMessageChecks(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	if err := os.Mkdir("docs", 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"message.md", "docs/schema.md"} {
		if err := os.WriteFile(name, []byte("`REQUEST_CHANGES` is a schema value."), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"-document", "message.md"},
		{"-document", "docs/../message.md"},
		{"-document", filepath.Join(directory, "message.md")},
		{"-document", "-repository"},
		{"-document", "docs/schema.go"},
	} {
		var output, warnings bytes.Buffer
		if run(args, &output, &warnings) == nil || output.Len() != 0 {
			t.Fatalf("document context bypass accepted: %v", args)
		}
	}
	var output, warnings bytes.Buffer
	if err := run([]string{"-document", "docs/schema.md"}, &output, &warnings); err != nil {
		t.Fatal("legitimate technical schema rejected", err)
	}
	if err := os.WriteFile("message.md", []byte("REQUEST_CHANGES"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if run([]string{"message.md"}, &output, &warnings) == nil || output.Len() != 0 {
		t.Fatal("ordinary message bypassed policy")
	}
	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink("../message.md", "docs/link.md"); err != nil {
			t.Skip("symlinks unavailable")
		}
		output.Reset()
		if run([]string{"-document", "docs/link.md"}, &output, &warnings) == nil || output.Len() != 0 {
			t.Fatal("symlink bypassed document scope")
		}
	})
}
