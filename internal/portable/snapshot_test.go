package portable

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

func TestSnapshotRequiresTerminalOperation(t *testing.T) {
	for _, state := range []string{"RUNNING", "WAITING_FOR_ANSWER", "WAITING_FOR_APPROVAL", "BLOCKED", "USAGE_EXCEEDED", "COMPLETED", "CANCELLED"} {
		err := stopped(store.Workspace{Records: store.Records{Operation: &store.Operation{State: state}}})
		if terminal := state == "COMPLETED" || state == "CANCELLED"; terminal != (err == nil) {
			t.Fatalf("operation %s: %v", state, err)
		}
	}
}

func TestPortableOriginPreservesNestedNamespace(t *testing.T) {
	repo := "group/subgroup/project"
	for _, remote := range []string{"https://gitlab.example.com/" + repo + ".git",
		"git@gitlab.example.com:" + repo + ".git", "ssh://git@gitlab.example.com/" + repo + ".git"} {
		origin, err := safeOrigin(remote, repo)
		if err != nil || origin != "https://gitlab.example.com/"+repo+".git" {
			t.Fatal("portable origin lost its namespace", origin, err)
		}
		for _, other := range []string{"subgroup/project", "another/" + repo} {
			if _, err := safeOrigin(remote, other); err == nil {
				t.Fatal("portable snapshot accepted a different namespace", other)
			}
		}
	}
}

func TestPortableObjectDependenciesIncludeParentHistory(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) []byte {
		t.Helper()
		output, err := git(t.Context(), root, nil, args...)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	run("init")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PRIVATE_SETTING=local"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".env")
	run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "local configuration")
	run("rm", ".env")
	run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "remove local configuration")
	objects := run("rev-list", "--objects", "--no-object-names", "HEAD")
	content, err := git(t.Context(), root, strings.NewReader(string(objects)), "cat-file", "--batch")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPortableObjects(content); err == nil || !strings.Contains(err.Error(), "dotenv") {
		t.Fatal("prohibited paths in parent history were accepted", err)
	}
	for _, name := range []string{"normal", ".env.example", ".env", ".taxion"} {
		tree := []byte("100644 " + name + "\x00" + strings.Repeat("\x01", 20))
		object := append([]byte(fmt.Sprintf("%s tree %d\n", strings.Repeat("a", 40), len(tree))), tree...)
		object = append(object, '\n')
		err := checkPortableObjects(object)
		if (err != nil) != (name == ".env" || name == ".taxion") {
			t.Fatalf("unexpected tree dependency result for %s: %v", name, err)
		}
	}
	if err := checkPortableObjects([]byte("incomplete")); err == nil {
		t.Fatal("incomplete object stream accepted")
	}
}

func TestPortableMemoryReferencesRequireEntriesNotCurrentApproval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	mem, err := memory.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := mem.Put(memory.KindDecision, memory.Entry{Title: "historical",
		SourceRefs: []string{"human:1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Revoke(memory.KindDecision, id, "human", "superseded elsewhere"); err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(); err != nil {
		t.Fatal(err)
	}
	state := store.Workspace{Graph: core.TaskGraph{History: []core.GraphEvent{
		{Task: core.GraphTask{MemoryRefs: []string{fmt.Sprintf("decision:%d", id)}}},
	}}}
	if err := validateMemoryReferences(t.Context(), state, path, true); err != nil {
		t.Fatal("historical entry lost its original status", err)
	}
	if err := validateMemoryReferences(t.Context(), state, path, false); err == nil {
		t.Fatal("missing historical Memory accepted")
	}
	state.Graph.History[0].Task.MemoryRefs = []string{"decision:999999"}
	if err := validateMemoryReferences(t.Context(), state, path, true); err == nil {
		t.Fatal("missing Memory entry accepted")
	}
}

func emptySnapshot(t *testing.T) (string, string, Report) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "workspace.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(store.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "portable.db")
	report, err := Create(t.Context(), path, filepath.Join(root, "missing-memory.db"), archive)
	if err != nil {
		t.Fatal(err)
	}
	return path, archive, report
}

func TestPortableArchiveRejectsCorruptionAndIncompleteManifest(t *testing.T) {
	for _, change := range []string{"blob", "missing", "version", "repository", "sqlite", "sidecar"} {
		t.Run(change, func(t *testing.T) {
			_, archive, report := emptySnapshot(t)
			if change == "sqlite" {
				if err := os.WriteFile(archive, []byte("not SQLite"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if change == "sidecar" {
				if err := os.WriteFile(archive+"-wal", nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				db, err := openSQL(archive, false)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "blob":
					_, err = db.Exec("UPDATE blobs SET content=x'01'")
				case "missing":
					_, err = db.Exec("DELETE FROM blobs")
				case "version", "repository":
					var content string
					if err := db.QueryRow("SELECT content FROM manifest").Scan(&content); err != nil {
						t.Fatal(err)
					}
					var m manifest
					if err := json.Unmarshal([]byte(content), &m); err != nil {
						t.Fatal(err)
					}
					m.Version = 3
					if change == "repository" {
						m.Version = 1
						m.Repositories = []repository{{ID: "owner/repo", Origin: "file:///private"}}
					}
					data, marshalErr := json.Marshal(m)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					_, err = db.Exec("UPDATE manifest SET content=?", string(data))
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Inspect(t.Context(), archive); err == nil {
				t.Fatal("invalid archive passed inspection")
			}
			destination := filepath.Join(t.TempDir(), "restore")
			if _, err := Restore(t.Context(), archive, Approval{Hash: report.Hash, Destination: destination,
				Actor: "human", Source: "human:approval"}); err == nil {
				t.Fatal("invalid archive restored")
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatal("failed restore left files")
			}
		})
	}
}

func TestPortableArchivePrivateFreshAndCancelable(t *testing.T) {
	path, archive, report := emptySnapshot(t)
	info, err := os.Stat(archive)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("archive is not private", err)
	}
	if _, err := Create(t.Context(), path, filepath.Join(t.TempDir(), "missing"), archive); err == nil {
		t.Fatal("snapshot replaced existing file")
	}
	data, err := os.ReadFile(archive)
	if err != nil || hash(data) != report.Hash {
		t.Fatal("existing snapshot changed", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	destination := filepath.Join(t.TempDir(), "canceled")
	if _, err := Restore(ctx, archive, Approval{Hash: report.Hash, Destination: destination,
		Actor: "human", Source: "human:approval"}); err == nil {
		t.Fatal("canceled restore succeeded")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("canceled restore left destination")
	}
	destination = filepath.Join(t.TempDir(), "restored")
	if _, err := Restore(t.Context(), archive, Approval{Hash: report.Hash, Destination: destination,
		Actor: "human", Source: "human:approval"}); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "second.db")
	if _, err := Create(t.Context(), filepath.Join(destination, ".taxion", "workspace.db"),
		filepath.Join(destination, ".taxion", "memory.db"), second); err != nil {
		t.Fatal("restored workspace cannot be snapshotted again", err)
	}
}

func TestPortableArchiveRejectsCredentialsWithoutPrintingThem(t *testing.T) {
	path, _, _ := emptySnapshot(t)
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret := "ghp_" + strings.Repeat("x", 40)
	if err := db.AppendRequestRecord("private", "planning", `{"input":"`+secret+`"}`); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "private.db")
	if _, err := Create(t.Context(), path, filepath.Join(t.TempDir(), "missing"), output); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("credential was accepted or printed")
	}
	memPath := filepath.Join(t.TempDir(), "memory.db")
	mem, err := memory.Open(memPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Put(memory.KindDecision, memory.Entry{Title: "private", Body: secret, SourceRefs: []string{"human:1"}}); err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(); err != nil {
		t.Fatal(err)
	}
	clean, _, _ := emptySnapshot(t)
	if _, err := Create(t.Context(), clean, memPath, output); err == nil {
		t.Fatal("Memory credential was accepted")
	}
}
