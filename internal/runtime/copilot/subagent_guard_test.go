package copilot

import (
	"testing"
	sdk "github.com/github/copilot-sdk/go"
)

func TestSubagentGuardRequiresBothTrackingStreamsToFinish(t *testing.T) {
	g:=newSubagentGuard(true); inv:=sdk.HookInvocation{SessionID:"root"}
	h:=&managedSession{subagents:g}; no:=false
	if _,err:=h.onSubagentStart(sdk.SubagentStartHookInput{SessionID:"root",AgentID:"child"},inv); err!=nil { t.Fatal(err) }
	g.observeEvent(sdk.SessionEvent{ID:"start",Data:&sdk.SubagentStartedData{ToolCallID:"call",Resumable:&no}})
	if err:=g.check(); err==nil { t.Fatal("active child was not blocked") }
	if _,err:=h.onSubagentStop(sdk.SubagentStopHookInput{SessionID:"root",AgentID:"child"},inv); err!=nil { t.Fatal(err) }
	if err:=g.check(); err==nil { t.Fatal("Hook completion concealed active Lifecycle child") }
	g.observeEvent(sdk.SessionEvent{ID:"complete",Data:&sdk.SubagentCompletedData{ToolCallID:"call"}})
	if err:=g.check(); err!=nil { t.Fatalf("both tracking streams clear: %v",err) }
}

func TestSubagentGuardRejectsBrokenCorrelations(t *testing.T) {
	t.Run("missing agent id",func(t *testing.T){g:=newSubagentGuard(true);g.hookStart(sdk.SubagentStartHookInput{SessionID:"root"},sdk.HookInvocation{SessionID:"root"});if g.check()==nil{t.Fatal("missing AgentID accepted")}})
	t.Run("unknown hook stop",func(t *testing.T){g:=newSubagentGuard(true);g.hookStop(sdk.SubagentStopHookInput{SessionID:"root",AgentID:"unknown"},sdk.HookInvocation{SessionID:"root"});if g.check()==nil{t.Fatal("unknown Hook stop accepted")}})
	t.Run("missing resumable",func(t *testing.T){g:=newSubagentGuard(true);g.observeEvent(sdk.SessionEvent{ID:"start",Data:&sdk.SubagentStartedData{ToolCallID:"call"}});if g.check()==nil{t.Fatal("unknown Resumable accepted")}})
	t.Run("completion without start",func(t *testing.T){g:=newSubagentGuard(true);g.observeEvent(sdk.SessionEvent{ID:"complete",Data:&sdk.SubagentCompletedData{ToolCallID:"missing"}});if g.check()==nil{t.Fatal("unmatched completion accepted")}})
	t.Run("duplicate start",func(t *testing.T){g:=newSubagentGuard(true);no:=false;for _,id:=range []string{"one","two"}{g.observeEvent(sdk.SessionEvent{ID:id,Data:&sdk.SubagentStartedData{ToolCallID:"same",Resumable:&no}})};if g.check()==nil{t.Fatal("duplicate start accepted")}})
}

func TestSubagentGuardRebuildExcludesOnlyConfirmedNonresumableChildren(t *testing.T) {
	tests:=[]struct{name string;resumable *bool;blocked bool}{
		{name:"nonresumable",resumable:boolPointer(false)},
		{name:"resumable",resumable:boolPointer(true),blocked:true},
		{name:"unknown",resumable:nil,blocked:true},
	}
	for _,tt:=range tests{t.Run(tt.name,func(t *testing.T){g:=newSubagentGuard(false);g.beginRebuild();g.rebuild([]sdk.SessionEvent{{ID:"start",Data:&sdk.SubagentStartedData{ToolCallID:"call",Resumable:tt.resumable}}});err:=g.check();if tt.blocked&&err==nil{t.Fatal("resumable or unknown child accepted")};if !tt.blocked&&err!=nil{t.Fatalf("confirmed nonresumable child blocked: %v",err)}})}
}
func boolPointer(v bool)*bool{return &v}
