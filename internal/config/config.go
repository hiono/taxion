package config

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version int `yaml:"version" json:"version"`
	Project struct {
		ID string `yaml:"id" json:"id"`
	} `yaml:"project" json:"project"`
	Repository struct {
		Provider      string `yaml:"provider" json:"provider"`
		Host          string `yaml:"host" json:"host"`
		Path          string `yaml:"path" json:"path"`
		Account       string `yaml:"account" json:"account"`
		CredentialRef string `yaml:"credentialRef" json:"credentialRef"`
	} `yaml:"repository" json:"repository"`
	Runtime struct {
		Provider      string `yaml:"provider" json:"provider"`
		Account       string `yaml:"account" json:"account"`
		CredentialRef string `yaml:"credentialRef" json:"credentialRef"`
		ContractRef   string `yaml:"contractRef" json:"contractRef"`
	} `yaml:"runtime" json:"runtime"`
	Execution struct {
		Requester   string `yaml:"requester" json:"requester"`
		Worker      string `yaml:"worker" json:"worker"`
		ApprovalRef string `yaml:"approvalRef" json:"approvalRef"`
	} `yaml:"execution" json:"execution"`
}

// Load checks configuration shape and references, not external authentication
// or the authority of an approval reference.
func Load(input []byte) (Config, error) {
	config, err := decode(input)
	if err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func decode(input []byte) (Config, error) {
	var config Config
	var document yaml.Node
	if err := yaml.Unmarshal(input, &document); err != nil {
		return Config{}, fmt.Errorf("invalid configuration YAML")
	}
	if err := checkTypes(&document, ""); err != nil {
		return Config{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(input))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("configuration requires known fields, unique keys, and the declared schema")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("configuration requires exactly one YAML document")
	}
	if err := config.validate(false); err != nil {
		return Config{}, err
	}
	return config, nil
}

func checkTypes(node *yaml.Node, path string) error {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := checkTypes(child, path); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("configuration keys require strings")
			}
			childPath := key.Value
			if path != "" {
				childPath = path + "." + key.Value
			}
			if err := checkTypes(value, childPath); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		want := "!!str"
		if path == "version" {
			want = "!!int"
		}
		if node.Tag != want {
			return fmt.Errorf("configuration values require strings except the integer version")
		}
		if want == "!!str" && node.Value == "" {
			return fmt.Errorf("explicit configuration values require nonempty strings")
		}
	default:
		return fmt.Errorf("configuration requires mappings and scalar values; aliases and sequences are unsupported")
	}
	return nil
}

func (c Config) Validate() error {
	return c.validate(true)
}

func (c *Config) fields() map[string]*string {
	return map[string]*string{
		"project.id":          &c.Project.ID,
		"repository.provider": &c.Repository.Provider, "repository.host": &c.Repository.Host,
		"repository.path": &c.Repository.Path, "repository.account": &c.Repository.Account,
		"repository.credentialRef": &c.Repository.CredentialRef,
		"runtime.provider":         &c.Runtime.Provider, "runtime.account": &c.Runtime.Account,
		"runtime.credentialRef": &c.Runtime.CredentialRef, "runtime.contractRef": &c.Runtime.ContractRef,
		"execution.requester": &c.Execution.Requester, "execution.worker": &c.Execution.Worker,
		"execution.approvalRef": &c.Execution.ApprovalRef,
	}
}

func (c Config) validate(required bool) error {
	if c.Version != 1 {
		return fmt.Errorf("configuration requires version 1")
	}
	for name, value := range c.fields() {
		if required && *value == "" || strings.ContainsAny(*value, " \t\r\n") {
			return fmt.Errorf("%s requires a nonempty identifier without whitespace", name)
		}
	}
	switch c.Repository.Provider {
	case "", "github", "gitlab", "bitbucket":
	default:
		return fmt.Errorf("repository.provider requires github, gitlab, or bitbucket")
	}
	if c.Runtime.Provider != "" && c.Runtime.Provider != "copilot" {
		return fmt.Errorf("runtime.provider requires copilot")
	}
	host, err := url.Parse("https://" + c.Repository.Host)
	if c.Repository.Host != "" && (err != nil || host.Hostname() == "" || host.Host != c.Repository.Host ||
		host.User != nil || host.Path != "" || host.RawQuery != "" || host.Fragment != "" ||
		strings.ContainsAny(c.Repository.Host, "\\?#")) {
		return fmt.Errorf("repository.host requires a hostname with an optional port")
	}
	parts := strings.Split(c.Repository.Path, "/")
	if c.Repository.Path == "" {
		return nil
	}
	if len(parts) < 2 || strings.ContainsAny(c.Repository.Path, "\\:?#%") {
		return fmt.Errorf("repository.path requires an owner or namespace and repository name")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("repository.path requires nonempty canonical path segments")
		}
	}
	return nil
}
