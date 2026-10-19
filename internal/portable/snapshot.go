// Package portable transfers stopped workspaces without provider credentials or
// rewriting their immutable contracts.
package portable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

type manifest struct {
	Version      int          `json:"version"`
	Workspace    string       `json:"workspace"`
	Memory       string       `json:"memory,omitempty"`
	Repositories []repository `json:"repositories"`
}

type Report struct {
	Hash         string `json:"hash"`
	Bytes        int64  `json:"bytes"`
	Repositories int    `json:"repositories"`
	Worktrees    int    `json:"worktrees"`
	Memory       bool   `json:"memory"`
}

type Approval struct {
	Hash        string
	Destination string
	Actor       string
	Source      string
}

func hash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func openSQL(path string, readonly bool) (*sql.DB, error) {
	location := url.URL{Scheme: "file", Path: path}
	if readonly {
		location.RawQuery = "mode=ro"
	}
	db, err := sql.Open("sqlite", location.String())
	if err == nil {
		err = db.Ping()
	}
	if err != nil && db != nil {
		db.Close()
	}
	return db, err
}

func stopped(state store.Workspace) error {
	for _, flow := range state.Graph.ReviewFlows {
		if flow.Status == "RUNNING" || flow.RunnerID != "" {
			return fmt.Errorf("snapshot requires stopped flows and released runner claims")
		}
	}
	for _, d := range state.Graph.Dispatches {
		if !slices.Contains([]core.DispatchStatus{core.DispatchSucceeded, core.DispatchFailed,
			core.DispatchCancelled, core.DispatchSuperseded}, d.Status) {
			return fmt.Errorf("snapshot requires every Dispatch to be reconciled and terminal")
		}
	}
	for _, update := range state.Records.Updates {
		if update.Status != "SENT" {
			return fmt.Errorf("snapshot requires external deliveries to be reconciled")
		}
	}
	return nil
}

// Create holds write reservations on both stores throughout collection. Git
// objects are immutable; pack-objects fails rather than omitting a missing root.
func Create(ctx context.Context, workspace, memoryPath, output string) (Report, error) {
	if !filepath.IsAbs(output) {
		return Report{}, fmt.Errorf("snapshot requires an absolute fresh output file")
	}
	tmp, err := os.MkdirTemp(filepath.Dir(output), ".taxion-portable-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(tmp)
	var locks []*sql.Conn
	var databases []*sql.DB
	defer func() {
		for _, connection := range locks {
			connection.ExecContext(context.Background(), "ROLLBACK")
			connection.Close()
		}
		for _, db := range databases {
			db.Close()
		}
	}()
	paths := []string{workspace}
	if _, err := os.Lstat(memoryPath); err == nil {
		paths = append(paths, memoryPath)
	} else if !os.IsNotExist(err) {
		return Report{}, err
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return Report{}, fmt.Errorf("snapshot sources must be existing regular databases")
		}
		db, err := openSQL(path, false)
		if err != nil {
			return Report{}, err
		}
		databases = append(databases, db)
		connection, err := db.Conn(ctx)
		if err != nil {
			return Report{}, err
		}
		locks = append(locks, connection)
		if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return Report{}, fmt.Errorf("reserve snapshot source: %w", err)
		}
	}
	if len(paths) == 1 {
		if _, err := os.Lstat(memoryPath); !os.IsNotExist(err) {
			return Report{}, fmt.Errorf("Memory appeared during collection; retry with both stores present")
		}
	}
	source, err := store.OpenReadOnly(workspace)
	if err != nil {
		return Report{}, err
	}
	initialized, initErr := source.WorkspaceInitialized()
	state, loadErr := source.LoadWorkspace()
	err = errors.Join(initErr, loadErr, source.Close())
	if err != nil {
		return Report{}, err
	}
	if !initialized {
		return Report{}, fmt.Errorf("portable snapshot requires an initialized workspace")
	}
	if err := stopped(state); err != nil {
		return Report{}, err
	}
	if content, err := json.Marshal(state); err != nil {
		return Report{}, err
	} else if err := checkCredentials(content); err != nil {
		return Report{}, err
	}
	archive := filepath.Join(tmp, "portable.db")
	db, err := openSQL(archive, false)
	if err != nil {
		return Report{}, err
	}
	defer db.Close()
	if err := os.Chmod(archive, 0600); err != nil {
		return Report{}, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE blobs(hash TEXT PRIMARY KEY,content BLOB NOT NULL);
		CREATE TABLE manifest(id INTEGER PRIMARY KEY CHECK(id=1),content TEXT NOT NULL)`); err != nil {
		return Report{}, err
	}
	add := func(data []byte) (string, error) {
		digest := hash(data)
		_, err := db.ExecContext(ctx, "INSERT INTO blobs VALUES(?,?) ON CONFLICT DO NOTHING", digest, data)
		return digest, err
	}
	m := manifest{Version: 1}
	for index, path := range paths {
		copyPath := filepath.Join(tmp, fmt.Sprintf("source-%d.db", index))
		reader, err := openSQL(path, true)
		if err != nil {
			return Report{}, err
		}
		_, vacuumErr := reader.ExecContext(ctx, "VACUUM INTO ?", copyPath)
		err = errors.Join(vacuumErr, reader.Close())
		if err != nil {
			return Report{}, err
		}
		if err := os.Chmod(copyPath, 0600); err != nil {
			return Report{}, err
		}
		if index == 1 {
			report, err := memory.InspectSnapshot(ctx, copyPath)
			if err != nil {
				return Report{}, err
			}
			for _, finding := range report.Findings {
				if finding.Rule == "key" || finding.Rule == "token" {
					return Report{}, fmt.Errorf("Memory contains credential-like content; local review is required")
				}
			}
		}
		data, err := os.ReadFile(copyPath)
		if err != nil {
			return Report{}, err
		}
		digest, err := add(data)
		if err != nil {
			return Report{}, err
		}
		if index == 0 {
			m.Workspace = digest
		} else {
			m.Memory = digest
		}
	}
	m.Repositories, err = collectRepositories(ctx, state, add)
	if err != nil {
		return Report{}, err
	}
	for _, repository := range m.Repositories {
		if repository.Endpoints != nil {
			m.Version = 2
			break
		}
	}
	content, err := json.Marshal(m)
	if err != nil {
		return Report{}, err
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO manifest VALUES(1,?)", string(content)); err != nil {
		return Report{}, err
	}
	if err := db.Close(); err != nil {
		return Report{}, err
	}
	report, err := Inspect(ctx, archive)
	if err != nil {
		return Report{}, err
	}
	if err := installFile(archive, output); err != nil {
		return Report{}, err
	}
	return report, nil
}

func installFile(source, destination string) error {
	file, err := os.OpenFile(source, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := os.Link(source, destination); err != nil {
		return fmt.Errorf("destination must not exist: %w", err)
	}
	parent, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return errors.Join(parent.Sync(), parent.Close())
}

func readArchive(input, directory string) (manifest, map[string][]byte, Report, error) {
	var m manifest
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() {
		return m, nil, Report{}, fmt.Errorf("portable snapshot requires a regular SQLite file")
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, err := os.Lstat(input + suffix); !os.IsNotExist(err) {
			return m, nil, Report{}, fmt.Errorf("portable snapshot has live or unreadable SQLite sidecars")
		}
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return m, nil, Report{}, err
	}
	path := filepath.Join(directory, "inspect.db")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return m, nil, Report{}, err
	}
	db, err := openSQL(path, true)
	if err != nil {
		return m, nil, Report{}, err
	}
	defer db.Close()
	var integrity, content string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return m, nil, Report{}, fmt.Errorf("portable snapshot integrity failure")
	}
	if err := db.QueryRow("SELECT content FROM manifest WHERE id=1").Scan(&content); err != nil {
		return m, nil, Report{}, err
	}
	if err := core.DecodeGraphInput(strings.NewReader(content), &m); err != nil {
		return m, nil, Report{}, err
	}
	if m.Version != 1 && m.Version != 2 {
		return m, nil, Report{}, fmt.Errorf("unsupported portable snapshot version")
	}
	blobs := map[string][]byte{}
	rows, err := db.Query("SELECT hash,content FROM blobs")
	if err != nil {
		return m, nil, Report{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var digest string
		var content []byte
		if err := rows.Scan(&digest, &content); err != nil {
			return m, nil, Report{}, err
		}
		if hash(content) != digest {
			return m, nil, Report{}, fmt.Errorf("portable blob checksum mismatch")
		}
		blobs[digest] = content
	}
	if err := rows.Err(); err != nil {
		return m, nil, Report{}, err
	}
	required := []string{m.Workspace}
	if m.Memory != "" {
		required = append(required, m.Memory)
	}
	report := Report{Hash: hash(data), Bytes: int64(len(data)), Repositories: len(m.Repositories), Memory: m.Memory != ""}
	seen := map[string]bool{}
	for _, repo := range m.Repositories {
		if seen[repo.ID] || repo.ID == "" {
			return m, nil, Report{}, fmt.Errorf("invalid portable repository identity")
		}
		seen[repo.ID] = true
		if err := checkSnapshotOrigin(m.Version, repo); err != nil {
			return m, nil, Report{}, err
		}
		required = append(required, repo.Pack)
		report.Worktrees += len(repo.Paths)
		for _, root := range repo.Roots {
			if !sha(root) {
				return m, nil, Report{}, fmt.Errorf("invalid portable object identity")
			}
		}
		for ref, tree := range repo.Refs {
			if !strings.HasPrefix(ref, "refs/taxion/snapshots/") || !sha(tree) ||
				len(strings.TrimPrefix(ref, "refs/taxion/snapshots/")) != 64 {
				return m, nil, Report{}, fmt.Errorf("invalid portable retention reference")
			}
		}
		for _, path := range repo.Paths {
			if !filepath.IsAbs(path.Original) || !sha(path.Head) || path.Tree != "" && !sha(path.Tree) {
				return m, nil, Report{}, fmt.Errorf("invalid portable worktree")
			}
		}
	}
	for _, digest := range required {
		if len(blobs[digest]) == 0 {
			return m, nil, Report{}, fmt.Errorf("portable snapshot is missing a required blob")
		}
	}
	referenced := map[string]bool{}
	for _, digest := range required {
		referenced[digest] = true
	}
	if len(referenced) != len(blobs) {
		return m, nil, Report{}, fmt.Errorf("portable snapshot contains unreferenced payloads")
	}
	return m, blobs, report, nil
}

func Inspect(ctx context.Context, input string) (Report, error) {
	tmp, err := os.MkdirTemp("", "taxion-portable-inspect-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(tmp)
	m, blobs, report, err := readArchive(input, tmp)
	if err != nil {
		return Report{}, err
	}
	_, err = unpack(ctx, tmp, tmp, m, blobs, nil)
	return report, err
}

// Restore publishes only into a fresh directory. Preparation uses the final
// absolute paths for Git worktree registration; failures remove that directory.
func Restore(ctx context.Context, input string, approval Approval) (report Report, err error) {
	if !filepath.IsAbs(approval.Destination) || approval.Actor == "" || approval.Source == "" || approval.Hash == "" {
		return report, fmt.Errorf("restore requires a fresh absolute destination and exact sourced approval")
	}
	if err := os.Mkdir(approval.Destination, 0700); err != nil {
		return report, err
	}
	complete := false
	defer func() {
		if !complete {
			err = errors.Join(err, os.RemoveAll(approval.Destination))
		}
	}()
	m, blobs, report, err := readArchive(input, approval.Destination)
	if err != nil {
		return report, err
	}
	if report.Hash != approval.Hash {
		return report, fmt.Errorf("snapshot differs from approved SHA256")
	}
	if _, err := unpack(ctx, approval.Destination, approval.Destination, m, blobs, &approval); err != nil {
		return report, err
	}
	if err := os.Remove(filepath.Join(approval.Destination, "inspect.db")); err != nil {
		return report, err
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := syncDirectoryTree(approval.Destination); err != nil {
		return report, err
	}
	complete = true
	return report, nil
}

func syncDirectoryTree(root string) error {
	var directories []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		return errors.Join(file.Sync(), file.Close())
	}); err != nil {
		return err
	}
	slices.Reverse(directories)
	directories = append(directories, filepath.Dir(root))
	for _, path := range directories {
		directory, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
			return err
		}
	}
	return nil
}

func validateMemoryReferences(ctx context.Context, state store.Workspace, path string, present bool) error {
	refs := map[string]bool{}
	for _, task := range state.Graph.Tasks {
		for _, ref := range task.MemoryRefs {
			refs[ref] = true
		}
	}
	for _, event := range state.Graph.History {
		for _, ref := range event.Task.MemoryRefs {
			refs[ref] = true
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		for _, ref := range dispatch.Instruction.MemoryRefs {
			refs[ref] = true
		}
	}
	if len(refs) == 0 {
		return nil
	}
	if !present {
		return fmt.Errorf("snapshot omits referenced Memory")
	}
	db, err := openSQL(path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	tables := map[string]string{"lesson": "lessons", "decision": "decisions", "guardrail": "guardrails", "concern": "concerns"}
	for ref := range refs {
		kind, id, _ := strings.Cut(ref, ":")
		table, ok := tables[kind]
		if !ok {
			return fmt.Errorf("unknown Memory reference kind")
		}
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE id=?", id).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("snapshot is missing a referenced Memory entry")
		}
	}
	return nil
}

func unpack(ctx context.Context, root, final string, m manifest, blobs map[string][]byte, approval *Approval) ([]store.RestoredPath, error) {
	stateDir := filepath.Join(root, ".taxion")
	if err := os.Mkdir(stateDir, 0700); err != nil {
		return nil, err
	}
	workspace := filepath.Join(stateDir, "workspace.db")
	if err := os.WriteFile(workspace, blobs[m.Workspace], 0600); err != nil {
		return nil, err
	}
	db, err := store.OpenReadOnly(workspace)
	if err != nil {
		return nil, err
	}
	state, loadErr := db.LoadWorkspace()
	err = errors.Join(loadErr, db.Close())
	if err != nil {
		return nil, err
	}
	if err := stopped(state); err != nil {
		return nil, err
	}
	if m.Memory != "" {
		path := filepath.Join(stateDir, "memory.db")
		if err := os.WriteFile(path, blobs[m.Memory], 0600); err != nil {
			return nil, err
		}
		if _, err := memory.InspectSnapshot(ctx, path); err != nil {
			return nil, err
		}
	}
	if err := validateMemoryReferences(ctx, state, filepath.Join(stateDir, "memory.db"), m.Memory != ""); err != nil {
		return nil, err
	}

	var mappings []store.RestoredPath
	seen := map[string]bool{}
	for index, repo := range m.Repositories {
		repoPath := filepath.Join(root, "repositories", fmt.Sprint(index))
		if err := os.MkdirAll(repoPath, 0700); err != nil {
			return nil, err
		}
		if _, err := git(ctx, repoPath, nil, "init", "--bare", "--template="); err != nil {
			return nil, err
		}
		if _, err := git(ctx, repoPath, bytes.NewReader(blobs[repo.Pack]), "index-pack", "--stdin", "--strict"); err != nil {
			return nil, err
		}
		for _, object := range repo.Roots {
			if _, err := git(ctx, repoPath, nil, "cat-file", "-e", object); err != nil {
				return nil, err
			}
			if _, err := git(ctx, repoPath, nil, "update-ref", "refs/taxion/portable/"+object, object); err != nil {
				return nil, err
			}
		}
		for ref, object := range repo.Refs {
			if _, err := git(ctx, repoPath, nil, "update-ref", ref, object); err != nil {
				return nil, err
			}
		}
		if _, err := git(ctx, repoPath, nil, "remote", "add", "origin", repo.Origin); err != nil {
			return nil, err
		}
		for i, path := range repo.Paths {
			if seen[path.Original] {
				return nil, fmt.Errorf("ambiguous portable path mapping")
			}
			seen[path.Original] = true
			local := filepath.Join(root, "worktrees", fmt.Sprintf("%d-%d", index, i))
			mappings = append(mappings, store.RestoredPath{Original: path.Original,
				Local: filepath.Join(final, "worktrees", fmt.Sprintf("%d-%d", index, i))})
			if path.Released {
				continue
			}
			args := []string{"worktree", "add", "--detach", local, path.Head}
			if path.Branch != "" {
				if _, err := git(ctx, repoPath, nil, "check-ref-format", "--branch", path.Branch); err != nil {
					return nil, err
				}
				args = []string{"worktree", "add", "-b", path.Branch, local, path.Head}
			}
			if _, err := git(ctx, repoPath, nil, args...); err != nil {
				return nil, err
			}
			tree := path.Tree
			if tree == "" {
				output, err := git(ctx, local, nil, "rev-parse", "HEAD^{tree}")
				if err != nil {
					return nil, err
				}
				tree = strings.TrimSpace(string(output))
			}
			if _, err := git(ctx, local, nil, "read-tree", "--reset", "-u", tree); err != nil {
				return nil, err
			}
			if _, err := git(ctx, local, nil, "fsck", "--no-reflogs"); err != nil {
				return nil, err
			}
		}
	}
	// Compare repository identities and immutable roots against the saved graph,
	// rather than trusting an archive manifest that omits a repository.
	check := state
	check.Records.Restore = &store.RestoreRecord{Paths: mappings}
	collected, err := collectRepositories(ctx, check, func(data []byte) (string, error) { return hash(data), nil })
	if err != nil {
		return nil, err
	}
	for i := range collected {
		// Pack ordering and compression are not part of Git object identity.
		collected[i].Pack = ""
	}
	expected := slices.Clone(m.Repositories)
	for i := range expected {
		expected[i].Pack = ""
	}
	if !reflect.DeepEqual(collected, expected) {
		return nil, fmt.Errorf("portable Git manifest differs from saved contracts and evidence")
	}
	if approval != nil {
		writable, err := store.Open(workspace)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, task := range state.Graph.Tasks {
			ids = append(ids, task.ID)
		}
		restore := &store.RestoreRecord{SnapshotHash: approval.Hash, Actor: approval.Actor,
			Source: approval.Source, TaskIDs: ids, Paths: mappings, At: time.Now().UTC()}
		// The archive's previous placement receipt is provenance, not permission
		// to write outside this fresh destination.
		err = writable.InstallRestoreRecord(restore)
		return mappings, errors.Join(err, writable.Close())
	}
	return mappings, nil
}
