package core

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestArchiveScopeProtectsDependenciesAndValidatesReceipts(t *testing.T) {
	parent, child := graphTask("parent"), graphTask("child")
	child.DependsOn = []TaskDependency{{TaskID: parent.ID, State: Completed}}
	graph := trackedGraph(t, parent, child)
	before := cloneGraph(graph)
	for _, ids := range [][]string{nil, {"missing"}, {"parent", "parent"}, {"parent"}} {
		if _, err := graph.ArchiveScope(ids); err == nil {
			t.Fatal("unsafe archive scope accepted", ids)
		}
	}
	ids, err := graph.ArchiveScope([]string{"child", "parent"})
	if err != nil || !reflect.DeepEqual(ids, []string{"child", "parent"}) || !reflect.DeepEqual(graph, before) {
		t.Fatal("batch scope was not deterministic and read-only", ids, err)
	}
	for _, bad := range []TaskArchive{
		{TaskIDs: ids}, {TaskIDs: []string{"missing"}, Actor: "human", Source: "human:discard", Reason: "obsolete", At: time.Now(), ExpectedHash: "sha256:abc"},
	} {
		copy := cloneGraph(graph)
		copy.Archives = []TaskArchive{bad}
		if err := copy.Validate(); err == nil {
			t.Fatal("invalid archive accepted", bad)
		}
	}
	graph.Archives = []TaskArchive{{TaskIDs: ids, Actor: "human", Source: "human:discard",
		Reason: "obsolete", At: time.Now(), ExpectedHash: "sha256:" + strings.Repeat("a", 64)}}
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	successor := graphTask("new-approved-work")
	successor.Supersedes = parent.ID
	if err := graph.Track(successor, "human", "new separately approved successor"); err != nil {
		t.Fatal("archive prevented a new successor", err)
	}
}
