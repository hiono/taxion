package gitguard

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeScanner(t *testing.T, behavior string) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "scanner")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf '1.9.0\\n'; exit 0; fi\n"
	switch behavior {
	case "clean":
		script += "cat >/dev/null\nprintf '[]'\n"
	case "leak":
		script += "cat >/dev/null\nprintf '[{\"Secret\":\"private-payload\",\"Match\":\"private-payload\"}]'\nexit 10\n"
	case "conditional":
		script += "content=$(cat)\ncase \"$content\" in *fake-private-payload*) printf '[{\"Secret\":\"private-payload\"}]'; exit 10;; esac\nprintf '[]'\n"
	case "error":
		script += "cat >/dev/null\nprintf 'private-payload' >&2\nexit 2\n"
	case "invalid":
		script += "cat >/dev/null\nprintf 'private-payload'\n"
	case "null":
		script += "cat >/dev/null\nprintf 'null'\n"
	case "mismatch":
		script += "cat >/dev/null\nprintf '[{}]'\n"
	case "environment":
		script += "cat >/dev/null\nif [ -n \"$BETTERLEAKS_CONFIG\" ] || [ -n \"$GH_TOKEN\" ] || [ -n \"$GITLEAKS_CONFIG_TOML\" ]; then exit 2; fi\nprintf '[]'\n"
	default:
		t.Fatal("unknown fake scanner")
	}
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return executable
}

func TestSecretScannerProtocolAndFailures(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, root, "source.txt", "indexed input")
	gitTest(t, root, "add", "source.txt")
	for _, behavior := range []string{"clean", "leak", "error", "invalid", "null", "mismatch", "environment"} {
		t.Run(behavior, func(t *testing.T) {
			t.Setenv("BETTERLEAKS_CONFIG", "untrusted-config")
			t.Setenv("GITLEAKS_CONFIG_TOML", "untrusted-config")
			t.Setenv("GH_TOKEN", "private-payload")
			before := gitTest(t, root, "ls-files", "--stage")
			err := CheckSecrets(context.Background(), root, fakeScanner(t, behavior))
			wantError := behavior != "clean" && behavior != "environment"
			if (err != nil) != wantError {
				t.Fatal("incorrect scanner result", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-payload") {
				t.Fatal("scanner result leaked private output")
			}
			if before != gitTest(t, root, "ls-files", "--stage") {
				t.Fatal("scan changed index")
			}
		})
	}
	if err := CheckSecrets(context.Background(), root, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing scanner accepted")
	}
	wrongVersion := fakeScanner(t, "clean")
	if err := os.WriteFile(wrongVersion, []byte("#!/bin/sh\nprintf '2.0.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckSecrets(context.Background(), root, wrongVersion); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatal("unverified version accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CheckSecrets(ctx, root, fakeScanner(t, "clean")); err == nil {
		t.Fatal("cancelled scan accepted")
	}
}

func TestSecretsReadActualBlobNotWorkingFiles(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, root, "source.txt", "fake-private-payload")
	gitTest(t, root, "add", "source.txt")
	writeTest(t, root, "source.txt", "safe working copy")
	scanner := fakeScanner(t, "conditional")
	if err := CheckSecrets(context.Background(), root, scanner); err == nil {
		t.Fatal("clean working file hid staged secret")
	}
	gitTest(t, root, "add", "source.txt")
	writeTest(t, root, "source.txt", "fake-private-payload")
	if err := CheckSecrets(context.Background(), root, scanner); err != nil {
		t.Fatal("unstaged secret contaminated scan", err)
	}
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "alternate-index"))
	gitTest(t, root, "read-tree", "--empty")
	gitTest(t, root, "add", "source.txt")
	if err := CheckSecrets(context.Background(), root, scanner); err == nil {
		t.Fatal("alternate commit index ignored")
	}
}

func TestSecretPathPolicyPrecedesScanner(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, root, ".env", "fake-private-payload")
	gitTest(t, root, "add", ".env")
	if err := CheckSecrets(context.Background(), root, "missing-scanner"); err == nil ||
		!strings.Contains(err.Error(), "prohibited local paths") {
		t.Fatal("path policy was weakened by scanner", err)
	}
}

func TestSecretScanRejectsMissingObjectsAndIgnoresReplacementBlobs(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, root, "source.txt", "fake-private-payload")
	gitTest(t, root, "add", "source.txt")
	original := strings.TrimSpace(gitTest(t, root, "hash-object", "source.txt"))
	writeTest(t, root, "clean.txt", "clean content")
	replacement := strings.TrimSpace(gitTest(t, root, "hash-object", "-w", "clean.txt"))
	gitTest(t, root, "replace", original, replacement)
	if err := CheckSecrets(context.Background(), root, fakeScanner(t, "conditional")); err == nil {
		t.Fatal("Git replacement hid original committed content")
	}
	gitTest(t, root, "update-index", "--cacheinfo", "100644,"+strings.Repeat("f", 40)+",source.txt")
	if err := CheckSecrets(context.Background(), root, fakeScanner(t, "clean")); err == nil ||
		!strings.Contains(err.Error(), "complete indexed content") {
		t.Fatal("missing indexed object accepted", err)
	}
}

func TestPinnedBetterleaksOfflineEvaluation(t *testing.T) {
	scanner := os.Getenv("TAXION_TEST_BETTERLEAKS")
	if scanner == "" {
		t.Skip("set TAXION_TEST_BETTERLEAKS to a verified Betterleaks 1.9.0 binary")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	writeTest(t, root, "environment.example", "TAXION_RUNTIME_ACCOUNT=developer\n")
	gitTest(t, root, "add", "environment.example")
	if err := CheckSecrets(context.Background(), root, scanner); err != nil {
		t.Fatal("safe configuration example rejected", err)
	}
	random := rand.New(rand.NewSource(72))
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var token strings.Builder
	token.WriteString("ghp_")
	for range 36 {
		token.WriteByte(alphabet[random.Intn(len(alphabet))])
	}
	// This deterministic fixture is not an issued credential; no validation is enabled.
	payload := "token = \"" + token.String() + "\" // betterleaks:allow\n"
	writeTest(t, root, "source.go", payload)
	gitTest(t, root, "add", "source.go")
	writeTest(t, root, ".betterleaksignore", "*\n")
	writeTest(t, root, ".betterleaks.toml", "invalid project configuration")
	t.Setenv("BETTERLEAKS_CONFIG", filepath.Join(root, ".betterleaks.toml"))
	if err := CheckSecrets(context.Background(), root, scanner); err == nil ||
		strings.Contains(err.Error(), token.String()) {
		t.Fatal("scanner failed to reject indexed fixture safely", err)
	}
	writeTest(t, root, "source.go", "package source\n")
	if err := CheckSecrets(context.Background(), root, scanner); err == nil {
		t.Fatal("clean working copy hid scanner fixture")
	}
	gitTest(t, root, "add", "source.go")
	if err := CheckSecrets(context.Background(), root, scanner); err != nil {
		t.Fatal("clean staged tree rejected", err)
	}
	writeTest(t, root, "go.sum", payload)
	gitTest(t, root, "add", "go.sum")
	if err := CheckSecrets(context.Background(), root, scanner); err != nil {
		t.Fatal("pinned default path filter changed", err)
	}
	writeTest(t, root, "source.go", payload)
	gitTest(t, root, "add", "source.go")
	if err := CheckSecrets(context.Background(), root, scanner); err == nil {
		t.Fatal("duplicate blob in excluded path hid source finding")
	}
}
