package specsearch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/tools"
)

func TestApprovedMarkdownSearchRetainsSourceAndProviderPermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.md")
	body := "# Requirements\r\n\r\nRetain the original contract.\r\nPreserve usage receipts.\r\n\r\nUnrelated scope stays unchanged.\r\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := tools.Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{EvidenceDirectory: t.TempDir(), Documents: []Document{{
		Asset: tools.Asset{Path: path, Hash: hash}, Title: "Requirements", Source: "synthetic:requirements",
		Version: "v1", Access: "synthetic",
	}}}
	scope = approved(t, scope)
	scope.Approval.Copilot, scope.Approval.OpenCode = false, true
	result, err := Search(context.Background(), scope, "contract")
	if err != nil || len(result.Specifications) != 1 {
		t.Fatal("Markdown search failed", err)
	}
	spec := result.Specifications[0]
	if spec.Location != "lines:3-4" || spec.DocumentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body))) ||
		spec.Source != "synthetic:requirements" || spec.Version != "v1" ||
		spec.Body != "Retain the original contract.\nPreserve usage receipts." ||
		strings.Contains(spec.Body, "Unrelated") {
		t.Fatalf("source identity or excerpt differs: %#v", spec)
	}
	if err := core.CheckSpecificationUse(result.Specifications, "opencode"); err != nil {
		t.Fatal(err)
	}
	if err := core.CheckSpecificationUse(result.Specifications, "copilot"); err == nil {
		t.Fatal("Markdown OpenCode approval leaked to Copilot")
	}
	if err := os.WriteFile(path, append([]byte(body), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(context.Background(), scope, "contract"); err == nil {
		t.Fatal("modified source accepted")
	}
}
