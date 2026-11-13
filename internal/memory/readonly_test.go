package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveDispatchContextReadOnly(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.json")
	path := PathForLedger(ledger)
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("opened a missing Memory database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read-only open created Memory", err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.Put(KindDecision, Entry{
		Title: "Keep acceptance", Body: "No execution", SourceRefs: []string{"user:decision"},
	})
	if err != nil || id != 1 {
		t.Fatal(id, err)
	}
	expected, err := db.DispatchContext([]string{"decision:1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := ResolveDispatchContextReadOnly(ledger, []string{"decision:1"})
	if err != nil || !reflect.DeepEqual(expected, actual) {
		t.Fatal(actual, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read-only context modified Memory", err)
	}
	readonly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if _, err := readonly.Put(KindDecision, Entry{Title: "forbidden"}); err == nil {
		t.Fatal("read-only Memory allowed writes")
	}
}
