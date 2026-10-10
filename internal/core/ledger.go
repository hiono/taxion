// Package core implements the SDK-neutral governance core of Taxion.
package core

import (
	"encoding/json"
	"os"
)

// Ledger is the durable record of tasks, dispatches, evidence, and
// checkpoints (LED-001). CLI/API use stdlib JSON; internal/store provides
// a separate SQLite backend preserving this shape.
type Ledger struct {
	Tasks       []Task       `json:"tasks"`
	Dispatches  []Dispatch   `json:"dispatches"`
	Evidence    []Evidence   `json:"evidence"`
	Checkpoints []Checkpoint `json:"checkpoints"`
}

// Save replaces the ledger via a same-directory rename so readers do not
// observe a partially written JSON file. Callers must serialize writers.
func (l Ledger) Save(path string) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadLedger reads a ledger written by Save. A missing file is an error;
// callers decide whether absence means "fresh start".
func LoadLedger(path string) (Ledger, error) {
	var l Ledger
	data, err := os.ReadFile(path)
	if err != nil {
		return Ledger{}, err
	}
	if err := json.Unmarshal(data, &l); err != nil {
		return Ledger{}, err
	}
	return l, nil
}
