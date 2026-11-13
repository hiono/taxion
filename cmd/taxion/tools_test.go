package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/specsearch"
	"github.com/hiono/taxion/internal/tools"
)

func TestSearchCLIMetadataAndExplicitSDKOptIn(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "synthetic.docx")
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	part, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("<root><p><t>Return 202. Synthetic body stays local.</t></p></root>")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	assetHash, err := tools.Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	scope := specsearch.Scope{EvidenceDirectory: filepath.Join(root, "private"), Documents: []specsearch.Document{{
		Asset: tools.Asset{Path: path, Hash: assetHash}, Title: "Synthetic CLI", Source: "synthetic:cli", Version: "v1", Access: "synthetic",
	}}}
	hash, err := scope.Hash()
	if err != nil {
		t.Fatal(err)
	}
	scope.Approval = &specsearch.Approval{Actor: "human", Source: "human:synthetic", Hash: hash}
	data, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	scopeFile := filepath.Join(root, "scope.json")
	if err := os.WriteFile(scopeFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := searchCmd([]string{"search", "--scope", scopeFile, "--query", "202"}, &output, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Synthetic body") || !strings.Contains(output.String(), `"attached":false`) ||
		!strings.Contains(output.String(), `"hash":"sha256:`) {
		t.Fatal("CLI disclosed body or omitted exact receipt", output.String())
	}
	for _, args := range [][]string{
		{"search", "--scope", scopeFile, "--sdk"},
		{"--scope", scopeFile, "--question", "202"},
		{"search", "--scope", scopeFile, "--query", ""},
	} {
		if err := searchCmd(args, &output, false); err == nil {
			t.Fatal("invalid flags acquired or paid for a search", args)
		}
	}
}
