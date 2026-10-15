package store

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

type documentString struct {
	Offset int    `json:"offset"`
	Hash   string `json:"hash"`
}

// Pool long JSON string tokens without re-encoding the surrounding document:
// RawMessage key order and the exact bytes of historical contracts must survive.
func poolDocumentStrings(tx *sql.Tx, data string) (string, string, error) {
	var content strings.Builder
	references := []documentString{}
	last := 0
	for start := 0; start < len(data); start++ {
		if data[start] != '"' {
			continue
		}
		end := start + 1
		for end < len(data) {
			if data[end] == '\\' {
				end += 2
			} else if data[end] == '"' {
				end++
				break
			} else {
				end++
			}
		}
		if end > len(data) {
			return "", "", fmt.Errorf("invalid workspace string")
		}
		if end-start >= 512 {
			token := data[start:end]
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
			compressed, err := compressWorkspaceBlob(token)
			if err != nil {
				return "", "", err
			}
			if _, err := tx.Exec(`INSERT INTO workspace_strings VALUES(?,?,?) ON CONFLICT(hash) DO NOTHING`, hash, compressed, len(token)); err != nil {
				return "", "", err
			}
			content.WriteString(data[last:start])
			references = append(references, documentString{content.Len(), hash})
			content.WriteString(`""`)
			last = end
		}
		start = end - 1
	}
	content.WriteString(data[last:])
	encoded, err := json.Marshal(references)
	return content.String(), string(encoded), err
}

func expandDocumentStrings(content, encoded string, pool map[string]string) (string, error) {
	var references []documentString
	if err := json.Unmarshal([]byte(encoded), &references); err != nil {
		return "", fmt.Errorf("invalid workspace string references: %w", err)
	}
	var data strings.Builder
	last := 0
	for _, ref := range references {
		token, ok := pool[ref.Hash]
		if !ok || ref.Offset < last || ref.Offset > len(content)-2 || content[ref.Offset:ref.Offset+2] != `""` {
			return "", fmt.Errorf("missing or invalid workspace string reference")
		}
		data.WriteString(content[last:ref.Offset])
		data.WriteString(token)
		last = ref.Offset + 2
	}
	data.WriteString(content[last:])
	return data.String(), nil
}

func loadDocumentStrings(q workspaceReader) (map[string]string, error) {
	rows, err := q.Query(`SELECT hash,content,bytes FROM workspace_strings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pool := map[string]string{}
	for rows.Next() {
		var hash, text string
		var compressed []byte
		var size int64
		if err := rows.Scan(&hash, &compressed, &size); err != nil {
			return nil, err
		}
		token, err := expandWorkspaceBlob(compressed, size)
		if err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(token), &text) != nil || hash != fmt.Sprintf("%x", sha256.Sum256([]byte(token))) {
			return nil, fmt.Errorf("workspace string integrity failure")
		}
		pool[hash] = token
	}
	return pool, rows.Err()
}

func compressWorkspaceBlob(text string) ([]byte, error) {
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := io.WriteString(writer, text); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

func expandWorkspaceBlob(compressed []byte, size int64) (string, error) {
	if size < 0 || size == math.MaxInt64 {
		return "", fmt.Errorf("invalid workspace content size")
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", fmt.Errorf("invalid compressed workspace content: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(reader, size+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || int64(len(raw)) != size {
		return "", fmt.Errorf("workspace content length or checksum failure")
	}
	return string(raw), nil
}
