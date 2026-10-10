package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Hash-chain (M8-G): every audit line carries prev, the hex SHA-256 of the
// exact previous line bytes (canonical JSON + '\n'). The first line omits
// prev (genesis). The chain detects modification, insertion, deletion, and
// reordering of covered lines — NOT silent tail truncation (a removed tail
// leaves a valid shorter chain; watch line counts externally).
//
// Old-line compat: lines without prev are "legacy" — valid only as a leading
// run before the first chained line (pre-M8-G logs keep working after
// upgrade; the first chained line links onto the last legacy line). A legacy
// line after a chained line is a gap and fails verification.
func chainHash(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

// tailHash returns the hash of the last non-empty line in the file, or ""
// when the file is missing/empty (genesis). It lets a restarted server
// continue the chain instead of forking a second genesis.
func tailHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return chainHash([]byte(lines[i] + "\n")), nil
		}
	}
	return "", nil
}

// Verify replays the chain in path. It returns the number of chained lines
// checked, the number of leading legacy lines skipped, and the 1-based line
// number of the first break (0 = OK).
func Verify(path string) (checked, legacy, badLine int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, err
	}
	raw := strings.Split(string(data), "\n")
	var lines []string
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	var prevLine []byte
	chained := false
	for i, l := range lines {
		var e struct {
			Prev string `json:"prev"`
		}
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			return checked, legacy, i + 1, nil
		}
		lineBytes := []byte(l + "\n")
		if e.Prev == "" {
			if chained {
				// Legacy line after chained lines: gap, not compat.
				return checked, legacy, i + 1, nil
			}
			legacy++
			prevLine = lineBytes
			continue
		}
		if i == 0 {
			// First line links to a rotated-away prefix we cannot see.
			return checked, legacy, 1, nil
		}
		if e.Prev != chainHash(prevLine) {
			return checked, legacy, i + 1, nil
		}
		chained = true
		checked++
		prevLine = lineBytes
	}
	return checked, legacy, 0, nil
}

// FormatVerify renders the human verdict for the audit verify command.
func FormatVerify(path string, checked, legacy, badLine int) string {
	if badLine != 0 {
		return fmt.Sprintf("audit: CHAIN BROKEN at line %d of %s (checked=%d legacy=%d) — treat every line from %d on as untrusted\n",
			badLine, path, checked, legacy, badLine)
	}
	return fmt.Sprintf("audit: chain OK in %s (checked=%d legacy=%d)\n", path, checked, legacy)
}
