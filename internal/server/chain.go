package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func chainHash(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

// Resume the existing tail across restarts; a new genesis would make a
// continuous audit file unverifiable.
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
// This proves internal consistency, not authenticity: tail truncation and
// a fully rewritten log with recomputed hashes can still verify.
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
				// Legacy prefixes allow pre-chain logs to survive upgrade.
				// After chaining starts, a missing link must not pass as legacy.
				return checked, legacy, i + 1, nil
			}
			legacy++
			prevLine = lineBytes
			continue
		}
		if i == 0 {
			// A missing predecessor cannot be verified from this file alone.
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
