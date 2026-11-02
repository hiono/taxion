package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func TestObservationRefreshesCommittedChangesAndRejectsInvalidState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	observe := func(want int) {
		t.Helper()
		if err := db.ObserveWorkspace(func(state Workspace) error {
			if len(state.Ledger.Tasks) != want {
				t.Fatalf("stale observation: %d != %d", len(state.Ledger.Tasks), want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	observe(0)
	snapshot := db.observation
	for range 100 {
		observe(0)
	}
	if db.observation != snapshot {
		t.Fatal("unchanged revision decoded historical state again")
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.UpdateWorkspace(func(state *Workspace) error {
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: "external"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	observe(1)
	if db.observation == snapshot {
		t.Fatal("external committed update reused old snapshot")
	}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: "local"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	observe(2)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	observe(2)
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("observation changed durable state", err)
	}
	if _, err := other.db.Exec(`UPDATE workspace_state SET revision=revision+1;
		UPDATE workspace_documents SET hash='invalid' WHERE area='ledger'`); err != nil {
		t.Fatal(err)
	}
	if err := db.ObserveWorkspace(func(Workspace) error { return nil }); err == nil {
		t.Fatal("changed invalid state was adopted")
	}
}

func processReadIO() map[string]float64 {
	data, _ := os.ReadFile("/proc/self/io")
	values := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			values[strings.TrimSuffix(fields[0], ":")], _ = strconv.ParseFloat(fields[1], 64)
		}
	}
	return values
}

// Same synthetic history and read pattern; full reproduces the RC13 poll.
func BenchmarkWorkspaceObservation(b *testing.B) {
	for _, history := range []int{0, 100, 1000} {
		for _, method := range []string{"full", "revision"} {
			b.Run(fmt.Sprintf("history=%d/%s", history, method), func(b *testing.B) {
				db, err := Open(filepath.Join(b.TempDir(), "workspace.db"))
				if err != nil {
					b.Fatal(err)
				}
				defer db.Close()
				state := Workspace{}
				for index := range history {
					state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: fmt.Sprint(index), Title: "historical task"})
					state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{ID: fmt.Sprint(index), TaskID: fmt.Sprint(index)})
					payload := strings.Repeat("saved history ", 300)
					state.Records.Events = append(state.Records.Events, RuntimeReceipt{
						DispatchID: fmt.Sprint(index), SessionID: "old-session", StreamID: "old-stream",
						Type: "output", Sequence: 1, Payload: payload,
						Hash: strings.Repeat("a", 64), At: time.Now().UTC(),
					})
				}
				if _, err := db.InitializeWorkspace(state); err != nil {
					b.Fatal(err)
				}
				if err := db.ObserveWorkspace(func(Workspace) error { return nil }); err != nil {
					b.Fatal(err)
				}
				before := processReadIO()
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if method == "full" {
						if _, err := db.LoadWorkspace(); err != nil {
							b.Fatal(err)
						}
					} else if err := db.ObserveWorkspace(func(Workspace) error { return nil }); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				after := processReadIO()
				for _, field := range []string{"rchar", "syscr", "read_bytes"} {
					if value, ok := before[field]; ok {
						b.ReportMetric((after[field]-value)/float64(b.N), field+"/op")
					}
				}
			})
		}
	}
}
