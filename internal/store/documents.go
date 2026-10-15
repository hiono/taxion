package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

const documentSchema = `
CREATE TABLE IF NOT EXISTS workspace_format (id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS workspace_documents (
  area TEXT NOT NULL, field TEXT NOT NULL, position INTEGER NOT NULL,
  content BLOB NOT NULL, hash TEXT NOT NULL, string_refs TEXT NOT NULL, bytes INTEGER NOT NULL,
  PRIMARY KEY(area,field,position));
CREATE TABLE IF NOT EXISTS workspace_strings (hash TEXT PRIMARY KEY, content BLOB NOT NULL, bytes INTEGER NOT NULL);
`

type workspaceReader interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

// Arrays are individual records; position -1 preserves scalar and empty values.
func workspaceDocuments(state Workspace) (map[[3]string]string, error) {
	areas := map[string]any{"ledger": state.Ledger, "graph": state.Graph, "records": state.Records}
	documents := map[[3]string]string{}
	for area, value := range areas {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		for field, raw := range fields {
			if len(raw) != 0 && raw[0] == '[' && string(raw) != "[]" {
				var items []json.RawMessage
				if err := json.Unmarshal(raw, &items); err != nil {
					return nil, err
				}
				for index, item := range items {
					documents[[3]string{area, field, fmt.Sprint(index)}] = string(item)
				}
			} else {
				documents[[3]string{area, field, "-1"}] = string(raw)
			}
		}
	}
	return documents, nil
}

func saveWorkspaceDocuments(tx *sql.Tx, state Workspace) error {
	documents, err := workspaceDocuments(state)
	if err != nil {
		return err
	}
	previous := map[[3]string]string{}
	rows, err := tx.Query(`SELECT area,field,position,hash FROM workspace_documents`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key [3]string
		var hash string
		if err := rows.Scan(&key[0], &key[1], &key[2], &hash); err != nil {
			rows.Close()
			return err
		}
		previous[key] = hash
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for key, content := range documents {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
		if old, ok := previous[key]; ok && old == hash {
			delete(previous, key)
			continue
		}
		pooled, refs, err := poolDocumentStrings(tx, content)
		if err != nil {
			return err
		}
		compressed, err := compressWorkspaceBlob(pooled)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO workspace_documents(area,field,position,content,hash,string_refs,bytes)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(area,field,position)
			DO UPDATE SET content=excluded.content,hash=excluded.hash,string_refs=excluded.string_refs,bytes=excluded.bytes`,
			key[0], key[1], key[2], compressed, hash, refs, len(pooled)); err != nil {
			return err
		}
		delete(previous, key)
	}
	for key := range previous {
		if _, err := tx.Exec(`DELETE FROM workspace_documents WHERE area=? AND field=? AND position=?`,
			key[0], key[1], key[2]); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM workspace_strings WHERE hash NOT IN (
		SELECT json_extract(value,'$.hash') FROM workspace_documents,json_each(string_refs))`); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO workspace_format VALUES(1,1) ON CONFLICT(id) DO NOTHING`)
	return err
}

func loadWorkspaceDocuments(q workspaceReader, state *Workspace) error {
	pool, err := loadDocumentStrings(q)
	if err != nil {
		return err
	}
	rows, err := q.Query(`SELECT area,field,position,content,hash,string_refs,bytes FROM workspace_documents ORDER BY area,field,position`)
	if err != nil {
		return err
	}
	defer rows.Close()
	areas := map[string]map[string]json.RawMessage{}
	arrays := map[[2]string][]json.RawMessage{}
	for rows.Next() {
		var area, field, hash, refs string
		var compressed []byte
		var size int64
		var position int
		if err := rows.Scan(&area, &field, &position, &compressed, &hash, &refs, &size); err != nil {
			return err
		}
		content, err := expandWorkspaceBlob(compressed, size)
		if err != nil {
			return err
		}
		content, err = expandDocumentStrings(content, refs, pool)
		if err != nil {
			return err
		}
		if hash != fmt.Sprintf("%x", sha256.Sum256([]byte(content))) || !json.Valid([]byte(content)) {
			return fmt.Errorf("workspace document integrity failure")
		}
		if area != "ledger" && area != "graph" && area != "records" {
			return fmt.Errorf("unknown workspace document area")
		}
		if areas[area] == nil {
			areas[area] = map[string]json.RawMessage{}
		}
		key := [2]string{area, field}
		if position == -1 {
			areas[area][field] = json.RawMessage(content)
		} else {
			if position != len(arrays[key]) {
				return fmt.Errorf("workspace document sequence is incomplete")
			}
			arrays[key] = append(arrays[key], json.RawMessage(content))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for key, items := range arrays {
		if _, exists := areas[key[0]][key[1]]; exists {
			return fmt.Errorf("workspace document has conflicting scalar and array values")
		}
		data, err := json.Marshal(items)
		if err != nil {
			return err
		}
		areas[key[0]][key[1]] = data
	}
	for area, target := range map[string]any{"ledger": &state.Ledger, "graph": &state.Graph, "records": &state.Records} {
		if areas[area] == nil {
			if area == "records" {
				continue
			}
			return fmt.Errorf("workspace document area %s is missing", area)
		}
		data, err := json.Marshal(areas[area])
		if err != nil {
			return err
		}
		if err := core.DecodeGraphInput(strings.NewReader(string(data)), target); err != nil {
			return err
		}
	}
	return state.Records.validate()
}

func documentFormat(q workspaceReader) (bool, error) {
	var exists int
	if err := q.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='workspace_format'`).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return false, nil
	}
	var version int
	err := q.QueryRow(`SELECT version FROM workspace_format WHERE id=1`).Scan(&version)
	if err == sql.ErrNoRows {
		return false, nil
	}

	if err != nil {
		return false, err
	}
	if version != 1 {
		return false, fmt.Errorf("unsupported workspace storage version %d", version)
	}
	return true, nil
}

func clearLegacyDocuments(tx *sql.Tx) error {
	if _, err := tx.Exec(`UPDATE workspace_state SET ledger_json='{}',graph_json='{}' WHERE id=1`); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE workspace_records SET records_json='{}' WHERE id=1`)
	return err
}

func (s *Store) migrateDocuments() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	normalized, err := documentFormat(tx)
	if err != nil || normalized {
		return err
	}
	var initialized int
	if err := tx.QueryRow(`SELECT count(*) FROM workspace_state WHERE id=1`).Scan(&initialized); err != nil {
		return err
	}
	if initialized == 0 {
		return nil
	}
	state, err := s.loadWorkspace(tx)
	if err != nil {
		return err
	}
	if err := saveWorkspaceDocuments(tx, state); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO workspace_records VALUES(1,'{}') ON CONFLICT(id) DO NOTHING`); err != nil {
		return err
	}
	if err := clearLegacyDocuments(tx); err != nil {
		return err
	}
	return tx.Commit()
}
