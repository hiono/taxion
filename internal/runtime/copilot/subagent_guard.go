package copilot

import (
	"fmt"
	"sync"

	sdk "github.com/github/copilot-sdk/go"
)

type subagentLifecycle struct {
	status string
	resumable *bool
}

type subagentGuard struct {
	mu sync.Mutex
	ready bool
	rebuilding bool
	pending []sdk.SessionEvent
	hookActive map[string]struct{}
	hookSeen map[string]struct{}
	hookFault bool
	lifecycle map[string]subagentLifecycle
	lifecycleSeen map[string]struct{}
	lifecycleFault bool
}

func newSubagentGuard(ready bool) *subagentGuard {
	return &subagentGuard{ready:ready, hookActive:map[string]struct{}{}, hookSeen:map[string]struct{}{}, lifecycle:map[string]subagentLifecycle{}, lifecycleSeen:map[string]struct{}{}}
}

func (a *Adapter) sessionHooks(handle *managedSession) *sdk.SessionHooks {
	var hooks *sdk.SessionHooks
	if a.config.ToolPolicy != nil { hooks = a.toolHooks() } else { hooks = &sdk.SessionHooks{} }
	hooks.OnSubagentStart = handle.onSubagentStart
	hooks.OnSubagentStop = handle.onSubagentStop
	return hooks
}

func (h *managedSession) subagentState() *subagentGuard {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subagents == nil { h.subagents = newSubagentGuard(false) }
	return h.subagents
}

func (h *managedSession) onSubagentStart(input sdk.SubagentStartHookInput, invocation sdk.HookInvocation) (*sdk.SubagentStartHookOutput,error) {
	h.subagentState().hookStart(input,invocation)
	return nil,nil
}
func (h *managedSession) onSubagentStop(input sdk.SubagentStopHookInput, invocation sdk.HookInvocation) (*sdk.SubagentStopHookOutput,error) {
	h.subagentState().hookStop(input,invocation)
	return nil,nil
}
func (g *subagentGuard) hookStart(input sdk.SubagentStartHookInput, invocation sdk.HookInvocation) {
	g.mu.Lock(); defer g.mu.Unlock()
	if input.SessionID=="" || input.SessionID!=invocation.SessionID || input.AgentID=="" { g.hookFault=true; return }
	if _,ok:=g.hookSeen[input.AgentID]; ok { g.hookFault=true; return }
	g.hookSeen[input.AgentID]=struct{}{}
	g.hookActive[input.AgentID]=struct{}{}
}
func (g *subagentGuard) hookStop(input sdk.SubagentStopHookInput, invocation sdk.HookInvocation) {
	g.mu.Lock(); defer g.mu.Unlock()
	if input.SessionID=="" || input.SessionID!=invocation.SessionID || input.AgentID=="" { g.hookFault=true; return }
	if _,ok:=g.hookActive[input.AgentID]; !ok { g.hookFault=true; return }
	delete(g.hookActive,input.AgentID)
}
func (g *subagentGuard) beginRebuild() {
	g.mu.Lock(); defer g.mu.Unlock()
	g.ready=false; g.rebuilding=true; g.pending=nil
}
func (g *subagentGuard) failRebuild() {
	g.mu.Lock(); defer g.mu.Unlock()
	g.rebuilding=false; g.pending=nil; g.lifecycleFault=true; g.ready=true
}
func isSubagentLifecycle(data sdk.SessionEventData) bool {
	switch data.(type) {
	case *sdk.SubagentStartedData,*sdk.SubagentCompletedData,*sdk.SubagentFailedData: return true
	default: return false
	}
}
func (g *subagentGuard) observeEvent(event sdk.SessionEvent) {
	if !isSubagentLifecycle(event.Data) { return }
	g.mu.Lock(); defer g.mu.Unlock()
	if g.rebuilding { g.pending=append(g.pending,event); return }
	g.applyLifecycleEvent(event)
}
func (g *subagentGuard) applyLifecycleEvent(event sdk.SessionEvent) {
	if event.ID=="" { g.lifecycleFault=true; return }
	if _,ok:=g.lifecycleSeen[event.ID]; ok { return }
	g.lifecycleSeen[event.ID]=struct{}{}
	switch d:=event.Data.(type) {
	case *sdk.SubagentStartedData:
		if d.ToolCallID=="" { g.lifecycleFault=true; return }
		if _,ok:=g.lifecycle[d.ToolCallID]; ok { g.lifecycleFault=true; return }
		var resumable *bool
		if d.Resumable!=nil { v:=*d.Resumable; resumable=&v } else { g.lifecycleFault=true }
		g.lifecycle[d.ToolCallID]=subagentLifecycle{status:"active",resumable:resumable}
	case *sdk.SubagentCompletedData: g.finishLifecycle(d.ToolCallID)
	case *sdk.SubagentFailedData: g.finishLifecycle(d.ToolCallID)
	}
}
func (g *subagentGuard) finishLifecycle(id string) {
	if id=="" { g.lifecycleFault=true; return }
	child,ok:=g.lifecycle[id]
	if !ok || child.status!="active" { g.lifecycleFault=true; return }
	child.status="finished"; g.lifecycle[id]=child
}

// A child is excluded after restart only when the SDK explicitly confirms
// that the child cannot be resumed.
func (g *subagentGuard) rebuild(events []sdk.SessionEvent) {
	g.mu.Lock(); defer g.mu.Unlock()
	g.lifecycle=map[string]subagentLifecycle{}
	g.lifecycleSeen=map[string]struct{}{}
	g.lifecycleFault=false
	for _,event:=range events { if isSubagentLifecycle(event.Data) { g.applyLifecycleEvent(event) } }
	for _,event:=range g.pending { g.applyLifecycleEvent(event) }
	g.pending=nil
	for id,child:=range g.lifecycle {
		if child.status=="active" && child.resumable!=nil && !*child.resumable { child.status="nonresumable"; g.lifecycle[id]=child }
	}
	g.rebuilding=false; g.ready=true
}
func (g *subagentGuard) check() error {
	g.mu.Lock(); defer g.mu.Unlock()
	if !g.ready || g.hookFault || g.lifecycleFault || len(g.hookActive)!=0 {
		return fmt.Errorf("taxion: clarification is blocked by incomplete or inconsistent subagent tracking")
	}
	for _,child:=range g.lifecycle { if child.status=="active" { return fmt.Errorf("taxion: clarification is blocked while a subagent may still be running") } }
	return nil
}
