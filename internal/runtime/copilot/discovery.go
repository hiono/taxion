package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/hiono/taxion/internal/tools"
	"gopkg.in/yaml.v3"
)

type DiscoveryPath struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Source string `json:"source"`
	State  string `json:"state"`
}

type DiscoveredAsset struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Source string `json:"source"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type DiscoveryReport struct {
	Backend       string            `json:"backend"`
	CLI           string            `json:"cli"`
	Version       string            `json:"version"`
	Worktree      string            `json:"worktree"`
	BaseDirectory string            `json:"baseDirectory"`
	Mode          string            `json:"mode"`
	Paths         []DiscoveryPath   `json:"paths"`
	Assets        []DiscoveredAsset `json:"assets"`
	Problems      []string          `json:"problems"`
	Limitations   []string          `json:"limitations"`
}

func (a *Adapter) inspectCommand(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, a.config.CLIPath, append([]string{"--no-auto-update"}, args...)...)
	command.Dir = a.config.WorkingDirectory
	command.Env = []string{}
	for _, item := range a.environment {
		if !strings.HasPrefix(item, "COPILOT_HOME=") {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "COPILOT_HOME="+a.config.BaseDirectory)
	body, err := command.Output()
	if err != nil {
		// Native diagnostics may contain credentials or complete server configs.
		return nil, fmt.Errorf("copilot %s inspection failed; diagnostic values withheld: %w", strings.Join(args, " "), err)
	}
	return body, nil
}

// Inspect uses only native inventory commands, never session creation or Send.
func (a *Adapter) Inspect(ctx context.Context) (DiscoveryReport, error) {
	report := DiscoveryReport{
		Backend: "copilot", CLI: a.config.CLIPath, Worktree: a.config.WorkingDirectory,
		BaseDirectory: a.config.BaseDirectory, Mode: "native-cli-defaults",
		Paths: []DiscoveryPath{}, Assets: []DiscoveredAsset{}, Problems: []string{},
		Limitations: []string{
			"Inventory is not proof of model selection, successful skill loading, or working server connections.",
			"Role tool permissions and file-access restrictions still apply; native file hooks are host code, not sandboxed by those callbacks.",
			"Path-specific instructions apply only to matching files. Missing native metadata is not inferred.",
			"Authentication, paid service access and contract eligibility are not checked.",
			"Remote organization customizations and resident-session caches are not queried; detected files are not a loaded-session inventory.",
		},
	}
	var failures []error
	fail := func(err error) {
		report.Problems = append(report.Problems, err.Error())
		failures = append(failures, err)
	}
	version, err := a.inspectCommand(ctx, "--version")
	if err != nil {
		fail(err)
	} else {
		report.Version = strings.TrimSpace(string(version))
	}
	if a.config.ToolPolicy != nil {
		report.Mode = "pinned-tools-policy"
		if err := a.config.ToolPolicy.Check(); err != nil {
			fail(err)
		}
		for _, group := range []struct {
			kind   string
			assets []tools.Asset
		}{{"skill", a.config.ToolPolicy.Skills}, {"plugin", a.config.ToolPolicy.Plugins}} {
			for _, asset := range group.assets {
				path := asset.Path
				report.Paths = append(report.Paths, observeDiscoveryPath(group.kind, path, "Task policy"))
				report.Assets = append(report.Assets, DiscoveredAsset{
					Kind: group.kind, Name: filepath.Base(path), Path: path, Source: "Task policy",
					State: "pinned", Reason: "Content hash is checked before execution; ambient discovery is disabled.",
				})
			}
		}
		for _, binding := range a.config.ToolPolicy.MCP {
			report.Assets = append(report.Assets, DiscoveredAsset{
				Kind: "mcp", Name: binding.Name, Source: "Task policy", State: "pinned",
				Reason: "Only the approved fixed request is available.",
			})
		}
		if a.config.ToolPolicy.RTK != nil {
			report.Assets = append(report.Assets, DiscoveredAsset{
				Kind: "rtk", Name: "rtk", Path: a.config.ToolPolicy.RTK.Path, Source: "Task policy", State: "pinned",
			})
		}
		return report, errors.Join(failures...)
	}
	for _, kind := range []string{"skill", "plugin", "instruction", "mcp", "lsp"} {
		body, err := a.inspectCommand(ctx, kind, "list", "--json")
		if err == nil {
			err = report.addNativeInventory(kind, body)
		}
		if err != nil {
			fail(err)
		}
	}
	if err := a.inspectLocalSources(&report); err != nil {
		fail(err)
	}
	sort.Slice(report.Assets, func(i, j int) bool {
		left, right := report.Assets[i], report.Assets[j]
		return left.Kind+"\x00"+left.Name+"\x00"+left.Path < right.Kind+"\x00"+right.Name+"\x00"+right.Path
	})
	for i := range report.Assets {
		asset := &report.Assets[i]
		if asset.Kind == "skill" && asset.State == "enabled" &&
			!slices.Contains(a.config.AvailableTools, "skill") {
			asset.State, asset.Reason = "unavailable", "The role tool list excludes skill."
		}
		if asset.Kind == "agent" && asset.State == "detected" &&
			!slices.Contains(a.config.AvailableTools, "task") &&
			!slices.Contains(a.config.AvailableTools, "run_subagent") &&
			!slices.Contains(a.config.AvailableTools, "agent") {
			asset.State, asset.Reason = "unavailable", "The role tool list has no custom-agent invocation tool."
		}
		if asset.Path != "" {
			path := observeDiscoveryPath(asset.Kind, asset.Path, "native inventory: "+asset.Source)
			if !slices.Contains(report.Paths, path) {
				report.Paths = append(report.Paths, path)
			}
		}
	}
	for i := range report.Assets {
		asset := &report.Assets[i]
		for j := range report.Assets {
			other := &report.Assets[j]
			if i == j || asset.Kind != other.Kind || asset.Name != other.Name {
				continue
			}
			if asset.Kind == "agent" && asset.Source == "project" && other.Source == "Copilot home" && other.State != "invalid" {
				asset.State, asset.Reason = "shadowed", "A personal agent with the same native ID has priority."
			} else if asset.Kind == "skill" {
				asset.Reason = "Multiple sources report this name; native runtime selection is not reimplemented by Taxion."
			}
		}
	}
	return report, errors.Join(failures...)
}

func (r *DiscoveryReport) addNativeInventory(kind string, body []byte) error {
	var entries []struct {
		Name            string `json:"name"`
		ID              string `json:"id"`
		Path            string `json:"path"`
		SourcePath      string `json:"sourcePath"`
		Source          string `json:"source"`
		Location        string `json:"location"`
		Enabled         *bool  `json:"enabled"`
		DefaultDisabled bool   `json:"defaultDisabled"`
	}
	if kind == "mcp" {
		var inventory struct {
			Servers map[string]struct {
				Source  string `json:"source"`
				Enabled *bool  `json:"enabled"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(body, &inventory); err != nil || inventory.Servers == nil {
			return fmt.Errorf("copilot mcp inventory has an unsupported format; values withheld")
		}
		for name, entry := range inventory.Servers {
			state := enabledState(entry.Enabled)
			r.Assets = append(r.Assets, DiscoveredAsset{Kind: kind, Name: name, Source: entry.Source,
				State: state, Reason: "Configured server only; connection and role tool availability are not verified."})
		}
		return nil
	}
	if err := json.Unmarshal(body, &entries); err != nil || entries == nil {
		return fmt.Errorf("copilot %s inventory has an unsupported format; values withheld", kind)
	}
	for _, entry := range entries {
		name, path, source := entry.Name, entry.Path, entry.Source
		state := enabledState(entry.Enabled)
		if kind == "instruction" {
			name, path, source = entry.ID, entry.SourcePath, entry.Location
			state = "conditional"
			if entry.DefaultDisabled {
				state = "disabled"
			}
		}
		if path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(r.Worktree, path)
		}
		if name == "" {
			return fmt.Errorf("copilot %s inventory omits an entry identity; values withheld", kind)
		}
		reason := ""
		if path == "" {
			reason = "Native inventory does not expose a resolved path; Taxion does not infer one."
		}
		r.Assets = append(r.Assets, DiscoveredAsset{Kind: kind, Name: name, Path: path, Source: source, State: state, Reason: reason})
	}
	return nil
}

func enabledState(enabled *bool) string {
	if enabled == nil {
		return "unknown"
	}
	if *enabled {
		return "enabled"
	}
	return "disabled"
}

func observeDiscoveryPath(kind, path, source string) DiscoveryPath {
	result := DiscoveryPath{Kind: kind, Path: path, Source: source, State: "present"}
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		result.State = "missing"
	} else if err != nil {
		result.State = "unreadable"
	}
	return result
}

func (a *Adapter) inspectLocalSources(report *DiscoveryReport) error {
	home := ""
	for _, item := range a.environment {
		if value, ok := strings.CutPrefix(item, "HOME="); ok {
			home = value
		}
	}
	if home == "" {
		return fmt.Errorf("copilot inspection requires the execution environment's HOME")
	}
	directories := []string{a.config.WorkingDirectory}
	for directory := a.config.WorkingDirectory; ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, ".git")); err == nil {
			for parent := filepath.Dir(a.config.WorkingDirectory); parent != filepath.Dir(directory); parent = filepath.Dir(parent) {
				if parent == filepath.Dir(parent) {
					break
				}
				if parent != a.config.WorkingDirectory {
					directories = append(directories, parent)
				}
			}
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("cannot determine execution repository boundary: %w", err)
		}
		if directory == filepath.Dir(directory) {
			break
		}
	}
	for _, directory := range directories {
		for _, relative := range []string{".agents/skills", ".github/skills", ".claude/skills",
			".github/agents", ".github/hooks", ".github/instructions",
			"AGENTS.md", "CLAUDE.md", ".claude/CLAUDE.md", "GEMINI.md",
			".github/copilot-instructions.md", ".mcp.json", ".github/mcp.json",
			".github/copilot/settings.json", ".github/copilot/settings.local.json"} {
			kind := "configuration"
			if strings.HasSuffix(relative, "/agents") {
				kind = "agent"
			} else if strings.HasSuffix(relative, "/hooks") {
				kind = "hook"
			} else if strings.HasSuffix(relative, "/skills") {
				kind = "skill"
			}
			report.Paths = append(report.Paths, observeDiscoveryPath(kind, filepath.Join(directory, relative), "project"))
		}
	}
	for _, relative := range []string{"config.json", "skills", "agents", "copilot-instructions.md", "instructions", "mcp-config.json", "installed-plugins"} {
		kind := "configuration"
		if relative == "agents" {
			kind = "agent"
		} else if relative == "skills" {
			kind = "skill"
		}
		report.Paths = append(report.Paths, observeDiscoveryPath(kind, filepath.Join(a.config.BaseDirectory, relative), "Copilot home"))
	}
	report.Paths = append(report.Paths, observeDiscoveryPath("skill", filepath.Join(home, ".agents/skills"), "personal"))
	for _, item := range a.environment {
		if value, ok := strings.CutPrefix(item, "COPILOT_CUSTOM_INSTRUCTIONS_DIRS="); ok {
			for _, directory := range strings.Split(value, ",") {
				if directory == "" {
					continue
				}
				if !filepath.IsAbs(directory) {
					directory = filepath.Join(a.config.WorkingDirectory, directory)
				}
				report.Paths = append(report.Paths, observeDiscoveryPath("instruction", directory, "COPILOT_CUSTOM_INSTRUCTIONS_DIRS"))
			}
		}
	}
	for _, path := range report.Paths {
		if path.State == "unreadable" {
			return fmt.Errorf("copilot search path is unreadable: %s", path.Path)
		}
		if path.State != "present" || path.Kind != "agent" && path.Kind != "hook" && path.Kind != "skill" {
			continue
		}
		walkRoot, err := filepath.EvalSymlinks(path.Path)
		if err != nil {
			return fmt.Errorf("cannot resolve copilot search path: %w", err)
		}
		err = filepath.WalkDir(walkRoot, func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || path.Kind == "agent" && !strings.HasSuffix(entry.Name(), ".md") ||
				path.Kind == "hook" && !strings.HasSuffix(entry.Name(), ".json") ||
				path.Kind == "skill" && entry.Name() != "SKILL.md" {
				return nil
			}
			relative, err := filepath.Rel(walkRoot, file)
			if err != nil {
				return err
			}
			resolvedFile := file
			file = filepath.Join(path.Path, relative)
			asset := DiscoveredAsset{Kind: path.Kind, Name: entry.Name(), Path: file, Source: path.Source,
				State: "detected", Reason: "File found; runtime activation is not verified."}
			if path.Kind == "agent" || path.Kind == "skill" {
				body, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				var header map[string]any
				parts := strings.SplitN(strings.ReplaceAll(string(body), "\r\n", "\n"), "---", 3)
				if len(parts) != 3 || strings.TrimSpace(parts[0]) != "" || yaml.Unmarshal([]byte(parts[1]), &header) != nil {
					asset.State, asset.Reason = "invalid", "Frontmatter is missing or malformed."
				} else {
					asset.Name = strings.ReplaceAll(filepath.ToSlash(strings.TrimSuffix(strings.TrimSuffix(relative, ".md"), ".agent")), "/", "--")
					asset.Reason = "Native custom-agent discovery; invocation remains subject to the role tool list."
				}
				if path.Kind == "skill" {
					for _, existing := range report.Assets {
						if existing.Kind != "skill" || existing.Path == "" {
							continue
						}
						existingPath, err := filepath.EvalSymlinks(existing.Path)
						if err == nil && (existingPath == resolvedFile || existingPath == filepath.Dir(resolvedFile)) {
							return nil
						}
					}
					asset.Name = filepath.Base(filepath.Dir(file))
					if asset.State != "invalid" {
						asset.State, asset.Reason = "not-listed", "SKILL.md exists but native inventory excludes it; activation is not inferred."
					}
				}
			} else {
				body, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				if !json.Valid(body) {
					asset.State, asset.Reason = "invalid", "Hook JSON is malformed."
				}
			}
			report.Assets = append(report.Assets, asset)
			return nil
		})
		if err != nil {
			return fmt.Errorf("cannot inspect copilot %s files: %w", path.Kind, err)
		}
	}
	return nil
}
