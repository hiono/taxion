package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/portable"
	"github.com/hiono/taxion/internal/store"
)

func TestUserGuidePortableExamplesWithoutSDK(t *testing.T) {
	guide, err := os.ReadFile("../../docs/userguide/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(guide), "## 別マシンへの一ファイル移送\n")
	if !ok {
		t.Fatal("guide omits portable workspace instructions")
	}
	previous := storePath
	storePath = filepath.Join(t.TempDir(), ".taxion", "ledger.json")
	source := filepath.Dir(storePath)
	t.Cleanup(func() { storePath = previous })
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "portable.db")
	destination := filepath.Join(t.TempDir(), "restored")
	replace := strings.NewReplacer("/srv/backups/taxion-transfer.db", archive, "/srv/taxion-restored", destination)
	commands := 0
	for _, block := range regexp.MustCompile("(?s)```sh\\n(.*?)\\n```").FindAllStringSubmatch(section, -1) {
		for _, line := range strings.Split(strings.ReplaceAll(block[1], "\\\n", ""), "\n") {
			args := strings.Fields(replace.Replace(line))
			if len(args) == 0 {
				continue
			}
			if args[0] != "taxion" {
				t.Fatalf("unexpected example command: %s", line)
			}
			commands++
			switch {
			case len(args) > 2 && args[1] == "workspace":
				for index, arg := range args {
					if arg == "CONFIRMED-SHA256" {
						report, err := portable.Inspect(t.Context(), archive)
						if err != nil {
							t.Fatal(err)
						}
						args[index] = report.Hash
					}
				}
				if err := workspaceMigrationCmd(args[2:]); err != nil {
					t.Fatalf("guide example %q: %v", line, err)
				}
				if args[2] == "snapshot" {
					if err := os.Rename(filepath.Dir(storePath), filepath.Dir(storePath)+"-offline"); err != nil {
						t.Fatal(err)
					}
				}
			case len(args) == 5 && args[1] == "--store" && args[3] == "records" && args[4] == "show":
				if args[2] != filepath.Join(destination, ".taxion", "ledger.json") {
					t.Fatal("receipt example does not select the restored ledger path")
				}
				storePath = args[2]
				if err := recordsCmd(args[4:]); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unsupported portable guide example: %s", line)
			}
		}
	}
	if commands != 5 {
		t.Fatalf("expected snapshot, both inspections, restore and receipt; got %d commands", commands)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("portable examples recreated or used the offline source", err)
	}
	db, err := store.OpenReadOnly(filepath.Join(destination, ".taxion", "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil || state.Records.Restore == nil || state.Records.Restore.Source != "human:approved-machine-transfer" {
		t.Fatal("guide commands did not persist exact approval", err)
	}
}

func TestWorkspaceMigrationCLI(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = previous })
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "portable.db")
	if err := workspaceMigrationCmd([]string{"snapshot", "--output", archive}); err != nil {
		t.Fatal(err)
	}
	if err := workspaceMigrationCmd([]string{"inspect-snapshot", "--file", archive}); err != nil {
		t.Fatal(err)
	}
	report, err := portable.Inspect(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	args := []string{"restore", "--file", archive, "--output", target,
		"--expected-hash", report.Hash, "--actor", "human", "--source", "human:move"}
	if err := workspaceMigrationCmd(args); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenReadOnly(filepath.Join(target, ".taxion", "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil || state.Records.Restore == nil || state.Records.Restore.SnapshotHash != report.Hash {
		t.Fatal("CLI did not persist restore approval", err)
	}
	if err := workspaceMigrationCmd(args); err == nil {
		t.Fatal("existing directory was replaced")
	}
	for _, invalid := range [][]string{
		nil, {"unknown"}, {"snapshot"}, {"snapshot", "--output", "relative"},
		{"inspect-snapshot", "--file", archive, "unexpected"},
		{"restore", "--file", archive, "--output", filepath.Join(t.TempDir(), "unapproved")},
	} {
		if err := workspaceMigrationCmd(invalid); err == nil {
			t.Fatal("invalid CLI accepted", invalid)
		}
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("restored directory is not private", err)
	}
}
