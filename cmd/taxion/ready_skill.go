package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"gopkg.in/yaml.v3"
)

const readySkillVersion = "1.3.2"
const readyCLIContract = "taxion.cli.v1"
const readySkillDirectory = ".agents/skills/taxion"
const readySkillPath = readySkillDirectory + "/SKILL.md"

// Explicit compatibility, not an assumption that every future minor is safe.
var acceptedReadySkillVersions = []string{readySkillVersion}

const readySkillBody = `# Taxion orchestration

Use this skill to operate Taxion for an approved coding task, inspect saved
results, or continue stopped work without repeating completed work.

Before operating Taxion, run taxion ready --check-skill. If it reports an
incompatible, stale, missing or modified skill, stop using this copy and report
the problem. Do not install or update it without the user's permission.
Then run taxion ready to obtain this binary's current operation contract.
Use taxion --help and taxion COMMAND --help for exact commands and options;
never guess flags, JSON fields or valid lifecycle transitions.

Keep the same executable environment for diagnosis and execution. Check required
executables before issuing work. --store selects the ledger path, not workspace.db.
Run internal Planner only when requested, not to repeat planning already done.
Read saved Task, Flow, Dispatch results and evidence before choosing another run.
For recovery, inspect workflow feedback-target and the saved partial/output tree;
do not fabricate a successful result, stop proof or user approval.
Preserve completed work. New budgets, changed scope and permissions need explicit
user approval. Controller validation and independent same-target review are
required before claiming completion. Inventory and skill integrity checks do not
prove authentication, authorization, model loading or successful execution.
`

type readySkillMetadata struct {
	Manager     string `yaml:"manager" json:"manager"`
	Format      string `yaml:"format" json:"format"`
	Version     string `yaml:"skill-version" json:"skillVersion"`
	CLIContract string `yaml:"cli-contract" json:"cliContract"`
	ReadyHash   string `yaml:"ready-sha256" json:"readyHash"`
	Producer    string `yaml:"producer" json:"producer"`
	ContentHash string `yaml:"content-sha256" json:"contentHash"`
}

type readySkillDocument struct {
	Name        string             `yaml:"name"`
	Description string             `yaml:"description"`
	Metadata    readySkillMetadata `yaml:"metadata"`
}

func readyDigest(body []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body))
}

func readyProducer() string {
	producer := "(devel)"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			producer = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" {
				producer += " " + setting.Key + "=" + setting.Value
			}
		}
	}
	return producer
}

func readySkillHash(document readySkillDocument, body string) (string, error) {
	document.Metadata.ContentHash = ""
	header, err := yaml.Marshal(document)
	if err != nil {
		return "", err
	}
	return readyDigest(append(header, []byte(body)...)), nil
}

func bundledReadySkill() ([]byte, error) {
	document := readySkillDocument{
		Name:        "taxion",
		Description: "Operate Taxion: approved coding, independent review, saved results and safe recovery.",
		Metadata: readySkillMetadata{
			Manager: "taxion", Format: "1", Version: readySkillVersion,
			CLIContract: readyCLIContract, ReadyHash: readyDigest([]byte(readyPrompt)),
			Producer: readyProducer(),
		},
	}
	var err error
	document.Metadata.ContentHash, err = readySkillHash(document, readySkillBody)
	if err != nil {
		return nil, err
	}
	header, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(header) + "---\n" + readySkillBody), nil
}

func parseReadySkill(data []byte) (readySkillDocument, error) {
	var document readySkillDocument
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return document, fmt.Errorf("ready: existing skill has no managed frontmatter; preserved")
	}
	header, body, ok := strings.Cut(string(data[4:]), "\n---\n")
	if !ok {
		return document, fmt.Errorf("ready: incomplete skill frontmatter; preserved")
	}
	decoder := yaml.NewDecoder(strings.NewReader(header))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return document, fmt.Errorf("ready: invalid managed skill metadata; preserved: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return document, fmt.Errorf("ready: extra skill metadata document; preserved")
	}
	if document.Name != "taxion" || document.Metadata.Manager != "taxion" ||
		document.Metadata.Format != "1" || document.Metadata.Producer == "" {
		return document, fmt.Errorf("ready: skill is not a supported Taxion-managed file; preserved")
	}
	canonical, err := yaml.Marshal(document)
	if err != nil {
		return document, err
	}
	if string(canonical) != header+"\n" {
		return document, fmt.Errorf("ready: skill frontmatter was edited or uses an unsupported format; preserved")
	}
	hash, err := readySkillHash(document, body)
	if err != nil {
		return document, err
	}
	if document.Metadata.ContentHash != hash {
		return document, fmt.Errorf("ready: skill content or metadata was modified; preserved")
	}
	return document, nil
}

func readySkillCompatibility(document readySkillDocument) error {
	accepted := false
	for _, version := range acceptedReadySkillVersions {
		accepted = accepted || document.Metadata.Version == version
	}
	if !accepted || document.Metadata.CLIContract != readyCLIContract {
		return fmt.Errorf("ready: incompatible skill version or CLI contract; use this binary's bundled skill")
	}
	if document.Metadata.ReadyHash != readyDigest([]byte(readyPrompt)) {
		return fmt.Errorf("ready: stale operation contract; explicit skill update required")
	}
	return nil
}

func openReadySkillHome() (*os.Root, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, "", err
	}
	for _, path := range []string{".agents", ".agents/skills", readySkillDirectory, readySkillPath} {
		info, err := root.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, "", fmt.Errorf("ready: cannot use skill path with unreadable or symlink components")
		}
	}
	return root, filepath.Join(home, readySkillPath), nil
}

func checkReadySkill(output io.Writer) error {
	report := struct {
		Path        string             `json:"path"`
		Producer    string             `json:"binaryBuild"`
		CLIContract string             `json:"cliContract"`
		Accepted    []string           `json:"acceptedSkillVersions"`
		Installed   readySkillMetadata `json:"installed"`
		Compatible  bool               `json:"compatible"`
		Problem     string             `json:"problem,omitempty"`
	}{Producer: readyProducer(), CLIContract: readyCLIContract, Accepted: acceptedReadySkillVersions}
	root, path, err := openReadySkillHome()
	report.Path = path
	if err == nil {
		defer root.Close()
		var data []byte
		data, err = root.ReadFile(readySkillPath)
		if err == nil {
			var document readySkillDocument
			document, err = parseReadySkill(data)
			if err == nil {
				report.Installed = document.Metadata
				err = readySkillCompatibility(document)
			}
		}
	}
	report.Compatible = err == nil
	if err != nil {
		report.Problem = err.Error()
	}
	return errors.Join(err, json.NewEncoder(output).Encode(report))
}

func installReadySkill(update bool, output io.Writer) error {
	data, err := bundledReadySkill()
	if err != nil {
		return err
	}
	root, path, err := openReadySkillHome()
	if err != nil {
		return err
	}
	defer root.Close()
	existing, err := root.ReadFile(readySkillPath)
	if err == nil {
		if _, err := parseReadySkill(existing); err != nil {
			return err
		}
		if bytes.Equal(existing, data) {
			_, err := fmt.Fprintf(output, "Taxion skill %s already installed: %s\n\n", readySkillVersion, path)
			return err
		}
		if !update {
			return fmt.Errorf("ready: existing managed skill differs; inspect --check-skill and explicitly use --update-skill; preserved")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if update {
		return fmt.Errorf("ready: skill is missing; use --install-skill")
	}
	if err := root.MkdirAll(readySkillDirectory, 0o755); err != nil {
		return err
	}
	temporary := readySkillDirectory + "/.taxion-skill-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		return err
	}
	// A first install must not replace a file created since the initial check.
	if existing == nil {
		if err := root.Link(temporary, readySkillPath); err != nil {
			return err
		}
	} else {
		current, err := root.ReadFile(readySkillPath)
		if err != nil || !bytes.Equal(current, existing) {
			return fmt.Errorf("ready: skill changed during update; preserved")
		}
		if err := root.Rename(temporary, readySkillPath); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(output, "Taxion skill %s installed: %s\nCLI contract: %s\n\n", readySkillVersion, path, readyCLIContract)
	return err
}
