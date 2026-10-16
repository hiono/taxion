// Package tools owns explicitly approved local extensions and their evidence.
package tools

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

const ApprovalKind = "taxion.tools.v1"

type Asset struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// MCPTool exposes one fixed request to a trusted local server. The server's
// process is approved host code, not sandboxed by SDK permission callbacks.
type MCPTool struct {
	Name      string          `json:"name"`
	Command   Asset           `json:"command"`
	Args      []string        `json:"args"`
	Inputs    []Asset         `json:"inputs,omitempty"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type Policy struct {
	Skills            []Asset   `json:"skills,omitempty"`
	Plugins           []Asset   `json:"plugins,omitempty"`
	MCP               []MCPTool `json:"mcp,omitempty"`
	RTK               *Asset    `json:"rtk,omitempty"`
	CompressionFilter string    `json:"compressionFilter,omitempty"`
	EvidenceDirectory string    `json:"evidenceDirectory"`
	AllowPublication  bool      `json:"allowPublication,omitempty"`
}

type Approval struct {
	Kind   string            `json:"kind"`
	TaskID string            `json:"taskId"`
	Actor  string            `json:"actor"`
	Roles  map[string]Policy `json:"roles"`
}

func CheckSkillAccess(policy *Policy, availableTools []string) error {
	if policy != nil && slices.Contains(availableTools, "skill") &&
		len(policy.Skills)+len(policy.Plugins) == 0 {
		return fmt.Errorf("taxion: skill tool requires approved skill or skill-only plugin assets")
	}
	return nil
}

func Resolve(decisions []core.TaskDecision, taskID, role string) (*Policy, error) {
	var selected *Policy
	seen := false
	for _, decision := range decisions {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal([]byte(decision.Text), &header) != nil || header.Kind != ApprovalKind {
			continue
		}
		if seen {
			return nil, fmt.Errorf("taxion: tools require one sourced approval")
		}
		seen = true
		var approval Approval
		if err := core.DecodeGraphInput(strings.NewReader(decision.Text), &approval); err != nil {
			return nil, fmt.Errorf("taxion: invalid tools approval")
		}
		if approval.TaskID != taskID || strings.TrimSpace(approval.Actor) == "" ||
			strings.TrimSpace(decision.Source) == "" || len(approval.Roles) == 0 {
			return nil, fmt.Errorf("taxion: tools require Task-bound Human approval with a source")
		}
		for approvedRole, policy := range approval.Roles {
			if !slices.Contains([]string{"CODER", "REVIEWER", "ORCHESTRATOR", "PLANNER"}, approvedRole) {
				return nil, fmt.Errorf("taxion: unsupported tools role")
			}
			if err := policy.CheckShape(); err != nil {
				return nil, err
			}
		}
		if policy, ok := approval.Roles[role]; ok {
			selected = &policy
		}
	}
	return selected, nil
}

// ResolveForTask follows only the controller-issued independent review link.
// Copying a decision into an unrelated Task never transfers its authority.
func ResolveForTask(graph core.TaskGraph, decisions []core.TaskDecision, taskID, role string) (*Policy, error) {
	sourceTask := taskID
	for _, decision := range decisions {
		var approval Approval
		if json.Unmarshal([]byte(decision.Text), &approval) != nil || approval.Kind != ApprovalKind || approval.TaskID == taskID {
			continue
		}
		authorized := false
		task, taskExists := graph.GraphTask(taskID)
		original, originalExists := graph.GraphTask(approval.TaskID)
		for _, flow := range graph.ReviewFlows {
			if !taskExists || !originalExists || role != "REVIEWER" || task.Type != "REVIEW" ||
				flow.TaskID != approval.TaskID || !slices.Contains(flow.Contract.Decisions, decision) ||
				!slices.Contains(original.Decisions, decision) {
				continue
			}
			for i := range flow.DispatchIDs {
				review, reviewExists := graph.GraphDispatch(flow.DispatchIDs[i])
				coderID, coderTaskID, createdFrom := flow.SourceDispatchID, flow.Contract.Supersedes, "flow:"+flow.ID
				if i > 0 {
					coderID, coderTaskID = flow.DispatchIDs[i-1], approval.TaskID
					createdFrom = "dispatch:" + coderID
				}
				coder, coderExists := graph.GraphDispatch(coderID)
				if reviewExists && coderExists && review.TaskID == taskID && review.Recipient == "REVIEWER" &&
					review.Relationship == core.RelationshipIndependent && review.RelatedID == coder.ID &&
					coder.Recipient == "CODER" && coder.Status == core.DispatchSucceeded && coder.TaskID == coderTaskID &&
					task.CreatedFrom == createdFrom && slices.Contains(review.Instruction.Decisions, decision) {
					authorized = true
				}
			}
		}
		if !authorized || sourceTask != taskID && sourceTask != approval.TaskID {
			return nil, fmt.Errorf("taxion: tools approval requires its original Task or its controller-issued independent review")
		}
		sourceTask = approval.TaskID
	}
	return Resolve(decisions, sourceTask, role)
}

func (p Policy) CheckShape() error {
	if p.RTK != nil && !slices.Contains([]string{"log", "cargo-test", "pytest", "git-diff"}, p.CompressionFilter) ||
		p.RTK == nil && p.CompressionFilter != "" {
		return fmt.Errorf("taxion: RTK requires an explicit supported presentation filter")
	}
	if !filepath.IsAbs(p.EvidenceDirectory) || filepath.Clean(p.EvidenceDirectory) != p.EvidenceDirectory ||
		p.EvidenceDirectory == string(filepath.Separator) {
		return fmt.Errorf("taxion: tools require an absolute private evidence directory")
	}
	assets := append(slices.Clone(p.Skills), p.Plugins...)
	if p.RTK != nil {
		assets = append(assets, *p.RTK)
	}
	names := map[string]bool{}
	for _, tool := range p.MCP {
		if !validName(tool.Name) || !strings.HasPrefix(tool.Name, "mcp_") || !validName(tool.Tool) || names[tool.Name] {
			return fmt.Errorf("taxion: MCP requires unique nonreserved exact tool names")
		}
		names[tool.Name] = true
		var args map[string]json.RawMessage
		if json.Unmarshal(tool.Arguments, &args) != nil || args == nil {
			return fmt.Errorf("taxion: MCP requires exact object arguments")
		}
		assets = append(assets, tool.Command)
		assets = append(assets, tool.Inputs...)
	}
	for _, asset := range assets {
		if !filepath.IsAbs(asset.Path) || filepath.Clean(asset.Path) != asset.Path ||
			len(asset.Hash) != len("sha256:")+64 || !strings.HasPrefix(asset.Hash, "sha256:") {
			return fmt.Errorf("taxion: extension assets require absolute paths and SHA256 hashes")
		}
	}
	return nil
}

// CheckPublication includes saved instructions and result provenance.
func CheckPublication(graph core.TaskGraph, taskID string) error {
	task, ok := graph.GraphTask(taskID)
	if !ok {
		return fmt.Errorf("taxion: tool publication requires a tracked Task")
	}
	seen := map[string]bool{}
	var check func([]core.TaskDecision, string) error
	check = func(decisions []core.TaskDecision, sourceTask string) error {
		roles := []string{"CODER", "REVIEWER", "ORCHESTRATOR", "PLANNER"}
		if task, ok := graph.GraphTask(sourceTask); ok && task.Type == "REVIEW" {
			roles = []string{"REVIEWER"}
		}
		for _, role := range roles {
			policy, err := ResolveForTask(graph, decisions, sourceTask, role)
			if err != nil {
				return err
			}
			if policy != nil && !policy.AllowPublication {
				return fmt.Errorf("taxion: tool-derived Task requires explicit publication approval")
			}
		}
		for _, decision := range decisions {
			if !strings.HasPrefix(decision.ID, "result:") {
				continue
			}
			id := strings.TrimPrefix(decision.ID, "result:")
			if seen[id] {
				continue
			}
			seen[id] = true
			dispatch, ok := graph.GraphDispatch(id)
			if !ok {
				return fmt.Errorf("taxion: tool publication provenance is unavailable")
			}
			if err := check(dispatch.Instruction.Decisions, dispatch.TaskID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(task.Decisions, taskID); err != nil {
		return err
	}
	for _, event := range graph.History {
		if event.TaskID == taskID {
			if err := check(event.Task.Decisions, taskID); err != nil {
				return err
			}
		}
	}
	for _, dispatch := range graph.Dispatches {
		if dispatch.TaskID == taskID {
			if err := check(dispatch.Instruction.Decisions, taskID); err != nil {
				return err
			}
		}
	}
	return nil
}

func validName(name string) bool {
	if name == "" || name == "*" {
		return false
	}
	for _, char := range name {
		if char != '_' && char != '-' && char != '.' &&
			!(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

// Hash includes relative paths, modes and bytes, and rejects symlinks and
// special files. Reading is bounded for both individual files and whole trees.
func Hash(path string) (string, error) {
	return hashAsset(path, "")
}

// CopyDirectory verifies the bytes being copied against the approved source
// hash. Fresh directories need not have the source's allocation size.
func CopyDirectory(asset Asset, destination string) error {
	if err := CheckAsset(asset, true); err != nil {
		return err
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return fmt.Errorf("taxion: asset copy requires a canonical absolute destination")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	hash, err := hashAsset(asset.Path, destination)
	if err != nil {
		return err
	}
	if hash != asset.Hash {
		return fmt.Errorf("taxion: extension asset changed while copying")
	}
	return CheckAsset(asset, true)
}

func hashAsset(path, destination string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("taxion: asset hash requires a canonical absolute path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", fmt.Errorf("taxion: asset path must be accessible without symlinks")
	}
	hash := sha256.New()
	var total int64
	type directory struct {
		path string
		mode fs.FileMode
	}
	var directories []directory
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("taxion: extension asset is inaccessible")
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("taxion: extension asset requires regular files and directories")
		}
		relative, err := filepath.Rel(path, current)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%q:%o:%d\n", filepath.ToSlash(relative), info.Mode(), info.Size())
		var target string
		if destination != "" {
			if info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				return fmt.Errorf("taxion: asset copy does not support special permission bits")
			}
			target = filepath.Join(destination, relative)
		}
		if entry.IsDir() {
			if target != "" {
				if err := os.MkdirAll(target, 0700); err != nil {
					return err
				}
				directories = append(directories, directory{target, info.Mode().Perm()})
			}
			return nil
		}
		if info.Size() > 256<<20 || total+info.Size() > 256<<20 {
			return fmt.Errorf("taxion: extension asset exceeds 256 MiB")
		}
		file, err := os.Open(current)
		if err != nil {
			return fmt.Errorf("taxion: extension asset is unreadable")
		}
		var output io.Writer = hash
		var copy *os.File
		if target != "" {
			copy, err = os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				file.Close()
				return err
			}
			output = io.MultiWriter(hash, copy)
		}
		count, readErr := io.Copy(output, io.LimitReader(file, (256<<20)-total+1))
		closeErr := file.Close()
		if copy != nil {
			modeErr := copy.Chmod(info.Mode().Perm())
			copyErr := copy.Close()
			if modeErr != nil || copyErr != nil {
				return fmt.Errorf("taxion: asset copy failed")
			}
		}
		total += count
		if readErr != nil || closeErr != nil || total > 256<<20 || count != info.Size() {
			return fmt.Errorf("taxion: extension asset observation failed")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	for _, directory := range slices.Backward(directories) {
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

func CheckAsset(asset Asset, directory bool) error {
	info, err := os.Lstat(asset.Path)
	if err != nil || info.IsDir() != directory || !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("taxion: approved extension asset is unavailable")
	}
	hash, err := Hash(asset.Path)
	if err != nil {
		return err
	}
	if hash != asset.Hash {
		return fmt.Errorf("taxion: extension asset changed; approve its current content")
	}
	return nil
}

func (p Policy) Check() error {
	if err := p.CheckShape(); err != nil {
		return err
	}
	for _, asset := range p.Skills {
		if err := CheckAsset(asset, true); err != nil {
			return err
		}
	}
	for _, asset := range p.Plugins {
		if err := CheckAsset(asset, true); err != nil {
			return err
		}
		file, err := os.Open(filepath.Join(asset.Path, "plugin.json"))
		if err != nil {
			return fmt.Errorf("taxion: skill-only plugin requires a root plugin.json")
		}
		var manifest struct {
			Schema      string `json:"$schema"`
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
		}
		parseErr := core.DecodeGraphInput(io.LimitReader(file, 64<<10), &manifest)
		closeErr := file.Close()
		if parseErr != nil || closeErr != nil || !validName(manifest.Name) ||
			manifest.Schema != "" && manifest.Schema != "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" {
			return fmt.Errorf("taxion: skill-only plugin requires an identity-only manifest")
		}
		// Skill-only plugins give the SDK its native loading path without
		// introducing agents, executable hooks, instructions or extra servers.
		if err := filepath.WalkDir(asset.Path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(asset.Path, path)
			if err != nil || relative == "." {
				return err
			}
			first := strings.Split(filepath.ToSlash(relative), "/")[0]
			if first != "skills" && relative != "plugin.json" {
				return fmt.Errorf("taxion: approved plugins contain a skills directory only")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	commands := []Asset{}
	for _, tool := range p.MCP {
		commands = append(commands, tool.Command)
		for _, asset := range tool.Inputs {
			info, err := os.Lstat(asset.Path)
			if err != nil {
				return fmt.Errorf("taxion: approved MCP input is unavailable")
			}
			if err := CheckAsset(asset, info.IsDir()); err != nil {
				return err
			}
		}
	}
	if p.RTK != nil {
		commands = append(commands, *p.RTK)
	}
	for _, command := range commands {
		if err := CheckAsset(command, false); err != nil {
			return err
		}
		info, err := os.Stat(command.Path)
		if err != nil || info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("taxion: approved extension command must be executable")
		}
	}
	return nil
}

func (p Policy) Clone() *Policy {
	data, _ := json.Marshal(p)
	var result Policy
	_ = json.Unmarshal(data, &result)
	return &result
}

func Equal(left, right *Policy) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return string(a) == string(b)
}
