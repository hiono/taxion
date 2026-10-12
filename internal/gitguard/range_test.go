package gitguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicationRangeScansDeletedSecretsAndMessages(t *testing.T) {
	for _, location := range []string{"blob", "message", "clean"} {
		t.Run(location, func(t *testing.T) {
			root := t.TempDir()
			gitTest(t, root, "init", "-q")
			gitTest(t, root, "config", "user.name", "fixture")
			gitTest(t, root, "config", "user.email", "fixture@example.test")
			writeTest(t, root, "source.txt", "safe")
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "-qm", "base")
			base := strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD"))
			content, message := "updated safe", "change"
			if location == "blob" {
				content = "fake-private-payload"
			}
			if location == "message" {
				message = "fake-private-payload"
			}
			writeTest(t, root, "source.txt", content)
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "-qm", message)
			writeTest(t, root, "source.txt", "safe final tree")
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "-qm", "remove intermediate content")
			head := strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD"))
			index := gitTest(t, root, "ls-files", "--stage")
			t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "wrong"))
			err := CheckRange(t.Context(), root, base, head, fakeScanner(t, "conditional"))
			if (err == nil) != (location == "clean") {
				t.Fatal("intermediate commit inspection", err)
			}
			if _, err := os.Stat(os.Getenv("GIT_INDEX_FILE")); !os.IsNotExist(err) {
				t.Fatal("ambient index used")
			}
			t.Setenv("GIT_INDEX_FILE", filepath.Join(root, ".git", "index"))
			if index != gitTest(t, root, "ls-files", "--stage") {
				t.Fatal("publication scan changed index")
			}
			if err := CheckRange(t.Context(), root, head, base, fakeScanner(t, "clean")); err == nil {
				t.Fatal("inverted range accepted")
			}
			if err := CheckRange(t.Context(), root, "HEAD", head, fakeScanner(t, "clean")); err == nil {
				t.Fatal("mutable range accepted")
			}
		})
	}
}
