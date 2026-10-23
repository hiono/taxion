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

func TestReleasePresentationUsesExactProfileAndStaticSource(t *testing.T) {
	profile, err := os.ReadFile("../../.github/communication-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	t.Chdir(directory)
	if err := os.WriteFile("profiles.json", profile, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_COMMUNICATION_PROFILES", filepath.Join(directory, "profiles.json"))
	sum := sha256.Sum256([]byte("reserved-private-fixture"))
	t.Setenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256", hex.EncodeToString(sum[:]))
	if err := os.WriteFile("VERSION", []byte("1.2.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	header := "version: 2\nrelease:\n  header: |\n    ## Fixed\n\n    設定内容を保持します。\nchangelog:\n  disable: true\n"
	if err := os.WriteFile(".goreleaser.yaml", []byte(header), 0600); err != nil {
		t.Fatal(err)
	}
	var output, warnings bytes.Buffer
	if err := run([]string{"-release", "v1.2.3"}, &output, &warnings); err != nil ||
		output.String() != "## Fixed\n\n設定内容を保持します。\n" {
		t.Fatal("release notes changed or unavailable", output.String(), err)
	}
	for _, input := range []string{
		strings.Replace(header, "disable: true", "disable: false", 1),
		strings.Replace(header, "設定内容を保持します。", "{{ .FutureResult }}", 1),
		strings.Replace(header, "設定内容を保持します。", "完了した結果は後続コメントに記載している", 1),
		strings.Replace(header, "設定内容を保持します。", "設定内容を保持します。 Setup choices are preserved.", 1),
		header + "unknown: [",
	} {
		if err := os.WriteFile(".goreleaser.yaml", []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		output.Reset()
		if run([]string{"-release", "v1.2.3"}, &output, &warnings) == nil || output.Len() != 0 {
			t.Fatal("unchecked release notes emitted")
		}
	}
}
