package gitproof

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/tools"
)

func TestDocumentSkillValidationPreflightWithoutModel(t *testing.T) {
	skillRoot := os.Getenv("TAXION_DOCUMENT_SKILL_ROOT")
	if skillRoot == "" {
		t.Skip("set TAXION_DOCUMENT_SKILL_ROOT to explicitly check local document skills without model use")
	}
	if !filepath.IsAbs(skillRoot) {
		t.Fatal("TAXION_DOCUMENT_SKILL_ROOT must be an absolute directory")
	}
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	hashes := map[string]string{}
	for _, name := range []string{"natural-japanese", "japanese-tech-writing", "cognitive-rhythm-writing"} {
		path := filepath.Join(skillRoot, name)
		if _, err := os.ReadFile(filepath.Join(path, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		hash, err := tools.Hash(path)
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = hash
		t.Logf("local skill %s %s", name, hash)
	}
	if _, err := exec.LookPath("uv"); err != nil {
		t.Fatal("document skill preflight requires an existing uv executable:", err)
	}
	root := t.TempDir()
	gitCommand(t, root, "init", "-b", "work/document")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "docs/guide.md", "# 保存先\n\n正式状態を SQLite に保存します。\n")
	head := commitFile(t, root, "docs/guide.md", "# 保存先\n\n正式状態と承認の履歴を SQLite に保存します。\n")
	// The host runs this exact authorized command; no SDK shell permission is added.
	command := `uv run --offline "$TAXION_DOCUMENT_SKILL_ROOT/natural-japanese/scripts/lint.py" --json --genre tech docs/guide.md`
	dispatch := core.GraphDispatch{
		ID: "document-preflight", TaskID: "document", Recipient: "CODER", Repository: "owner/repo",
		Instruction: core.DispatchInstruction{
			Version: core.DispatchInstructionVersion, Worktree: root, Branch: "work/document",
			BaselineSHA: baseline, AuthorizedActions: []string{"docs"},
			ValidationCommands: []string{command},
		},
	}
	result := &core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: "CODER", Repository: dispatch.Repository,
		Status: core.DispatchSucceeded, StartedSHA: baseline, OutputSHA: head,
		ChangedFiles: []string{"docs/guide.md"}, Evidence: []string{"model-free prerequisite check"},
		NextTransition: core.WaitingForEvidence,
	}
	if err := ExecuteValidations(t.Context(), dispatch, result); err != nil {
		t.Fatal("real skill script cannot run through authorized host validation:", err)
	}
	if len(result.Validations) != 1 || result.Validations[0].ExitCode != 0 ||
		result.Validations[0].StartedAt.IsZero() || result.Validations[0].SHA != head {
		t.Fatal("missing exact-artifact host validation evidence")
	}
	var report struct {
		File     string            `json:"file"`
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal([]byte(result.Validations[0].Output), &report); err != nil {
		t.Fatal("skill lint did not produce its JSON report:", err)
	}
	if report.File != "docs/guide.md" || report.Findings == nil || len(report.Findings) != 0 {
		t.Fatal("unexpected lint result for the minimal preflight document")
	}
	for name, expected := range hashes {
		actual, err := tools.Hash(filepath.Join(skillRoot, name))
		if err != nil || actual != expected {
			t.Fatal("preflight changed a local skill:", name, err)
		}
	}
	t.Log("real script executed against the exact Git artifact; no SDK session or skill loading was tested")
}
