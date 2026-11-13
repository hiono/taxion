package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxSnapshotBytes = 256 << 20

type SnapshotFinding struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Rule   string `json:"rule"`
	Count  int    `json:"count"`
}

type SnapshotReport struct {
	Hash     string            `json:"hash"`
	Bytes    int64             `json:"bytes"`
	Rows     map[string]int64  `json:"rows"`
	Findings []SnapshotFinding `json:"findings"`
}

// Snapshot uses SQLite's consistent online snapshot, including lifecycle,
// conflicts, approvals, settings, warning provenance and both entry scopes.
func (s *Store) Snapshot(ctx context.Context, output string) (report SnapshotReport, err error) {
	if !filepath.IsAbs(output) {
		return report, fmt.Errorf("taxion: snapshot requires an absolute local destination")
	}
	dir, err := os.MkdirTemp(filepath.Dir(output), ".taxion-memory-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "snapshot.db")
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return report, fmt.Errorf("taxion: Memory snapshot: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return report, err
	}
	report, err = InspectSnapshot(ctx, path)
	if err != nil {
		return report, err
	}
	if err := installSnapshot(path, output); err != nil {
		return report, fmt.Errorf("taxion: install private snapshot without replacing existing files: %w", err)
	}
	return report, nil
}

func readSnapshot(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSnapshotBytes {
		return nil, fmt.Errorf("taxion: Memory snapshot requires a regular file within 256 MiB")
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return nil, fmt.Errorf("taxion: snapshot has a live SQLite sidecar; create a consistent Memory snapshot first")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSnapshotBytes {
		return nil, fmt.Errorf("taxion: Memory snapshot requires a regular file within 256 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSnapshotBytes || len(data) < 16 || string(data[:16]) != "SQLite format 3\x00" {
		return nil, fmt.Errorf("taxion: invalid Memory SQLite snapshot")
	}
	return data, nil
}

func installSnapshot(path, destination string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := os.Link(path, destination); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// InspectSnapshot reports locations and pattern names, never matched text.
// It opens a private immutable copy so the hash and review describe one file.
func InspectSnapshot(ctx context.Context, path string) (SnapshotReport, error) {
	data, err := readSnapshot(path)
	if err != nil {
		return SnapshotReport{}, err
	}
	dir, err := os.MkdirTemp("", "taxion-memory-inspect-")
	if err != nil {
		return SnapshotReport{}, err
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "snapshot.db")
	if err := os.WriteFile(copyPath, data, 0600); err != nil {
		return SnapshotReport{}, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: copyPath}).String()+"?mode=ro")
	if err != nil {
		return SnapshotReport{}, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return SnapshotReport{}, fmt.Errorf("taxion: snapshot integrity check failed: %v", err)
	}
	tables, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name`)
	if err != nil {
		return SnapshotReport{}, err
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			tables.Close()
			return SnapshotReport{}, err
		}
		names = append(names, name)
	}
	if err := errors.Join(tables.Err(), tables.Close()); err != nil {
		return SnapshotReport{}, err
	}
	report := SnapshotReport{
		Hash: fmt.Sprintf("sha256:%x", sha256.Sum256(data)), Bytes: int64(len(data)),
		Rows: map[string]int64{}, Findings: []SnapshotFinding{},
	}
	for _, table := range names {
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+quoteIdentifier(table))
		if err != nil {
			return SnapshotReport{}, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return SnapshotReport{}, err
		}
		var count int64
		counts := map[string]int{}
		values, destinations := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(destinations...); err != nil {
				rows.Close()
				return SnapshotReport{}, err
			}
			count++
			for i, value := range values {
				var text string
				switch value := value.(type) {
				case string:
					text = value
				case []byte:
					text = string(value)
				}
				for _, rule := range DefaultRules {
					counts[columns[i]+"\x00"+rule.Name] += len(rule.Re.FindAllStringIndex(text, -1))
				}
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return SnapshotReport{}, err
		}
		report.Rows[table] = count
		for _, column := range columns {
			seen := map[string]bool{}
			for _, rule := range DefaultRules {
				key := column + "\x00" + rule.Name
				if counts[key] > 0 && !seen[rule.Name] {
					report.Findings = append(report.Findings, SnapshotFinding{table, column, rule.Name, counts[key]})
					seen[rule.Name] = true
				}
			}
		}
	}
	for _, table := range []string{"lessons", "decisions", "guardrails", "concerns", "memory_entry_events",
		"warnings", "warning_events", "memory_conflicts", "memory_settings", "memory_guardrail_checks"} {
		if _, ok := report.Rows[table]; !ok {
			return SnapshotReport{}, fmt.Errorf("taxion: snapshot lacks Memory table %s", table)
		}
	}
	return report, nil
}

type MigrationApproval struct {
	Hash             string `json:"hash"`
	Destination      string `json:"destination"`
	Actor            string `json:"actor"`
	Source           string `json:"source"`
	ApproveSensitive bool   `json:"approveSensitive"`
}

// RestoreSnapshot installs into a fresh destination. IDs and references remain
// intact; an existing Memory is kept for an explicit workspace replacement.
func RestoreSnapshot(ctx context.Context, input string, approval MigrationApproval) (SnapshotReport, error) {
	if !filepath.IsAbs(approval.Destination) || strings.TrimSpace(approval.Actor) == "" ||
		strings.TrimSpace(approval.Source) == "" || approval.Hash == "" {
		return SnapshotReport{}, fmt.Errorf("taxion: migration requires an absolute local destination and exact sourced approval")
	}
	data, err := readSnapshot(input)
	if err != nil {
		return SnapshotReport{}, err
	}
	if fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != approval.Hash {
		return SnapshotReport{}, fmt.Errorf("taxion: migration snapshot differs from the approved hash")
	}
	dir, err := os.MkdirTemp(filepath.Dir(approval.Destination), ".taxion-memory-")
	if err != nil {
		return SnapshotReport{}, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "memory.db")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return SnapshotReport{}, err
	}
	report, err := InspectSnapshot(ctx, path)
	if err != nil {
		return SnapshotReport{}, err
	}
	if len(report.Findings) > 0 && !approval.ApproveSensitive {
		return SnapshotReport{}, fmt.Errorf("taxion: migration has sanitizer findings; review them and explicitly approve sensitive local transfer")
	}
	// The receipt is local evidence inside the transferred store.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return SnapshotReport{}, err
	}
	record, err := json.Marshal(approval)
	if err != nil {
		db.Close()
		return SnapshotReport{}, err
	}
	_, writeErr := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS memory_migration_receipts (
		id INTEGER PRIMARY KEY, approval TEXT NOT NULL, restored_at TEXT NOT NULL);
		INSERT INTO memory_migration_receipts(approval,restored_at) VALUES(?,?)`, string(record), time.Now().UTC().Format(time.RFC3339Nano))
	if err := errors.Join(writeErr, db.Close()); err != nil {
		return SnapshotReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return SnapshotReport{}, err
	}
	if err := installSnapshot(path, approval.Destination); err != nil {
		return SnapshotReport{}, fmt.Errorf("taxion: restore keeps existing destinations intact: %w", err)
	}
	return report, nil
}
