package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hiono/taxion/internal/core"
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
		Provider       string `yaml:"provider" json:"provider"`
		Account        string `yaml:"account" json:"account"`
		CredentialRef  string `yaml:"credentialRef" json:"credentialRef"`
		ContractRef    string `yaml:"contractRef" json:"contractRef"`
		ACPCommand     string `yaml:"acpCommand,omitempty" json:"acpCommand,omitempty"`
		ACPCommandHash string `yaml:"acpCommandHash,omitempty" json:"acpCommandHash,omitempty"`
		ACPConfig      string `yaml:"acpConfig,omitempty" json:"acpConfig,omitempty"`
		ACPConfigHash  string `yaml:"acpConfigHash,omitempty" json:"acpConfigHash,omitempty"`
	} `yaml:"runtime" json:"runtime"`
	Execution struct {
		Requester      string `yaml:"requester" json:"requester"`
		Worker         string `yaml:"worker" json:"worker"`
		ApprovalRef    string `yaml:"approvalRef" json:"approvalRef"`
		BudgetGuidance string `yaml:"budgetGuidance,omitempty" json:"budgetGuidance,omitempty"`
	} `yaml:"execution" json:"execution"`
	Endpoints Endpoints            `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`
	Billing   core.BillingSettings `yaml:"billing,omitempty" json:"billing,omitempty"`
}

// Endpoints separates the addresses a GitLab instance serves from the project
// path. The API base, the web base, the HTTPS clone prefix, the SSH host and
// port, and the numeric project ID are distinct identities that legacy
// repository.host and repository.path cannot express.
type Endpoints struct {
	APIBase        string `yaml:"apiBase" json:"apiBase"`
	WebBase        string `yaml:"webBase" json:"webBase"`
	HTTPSCloneBase string `yaml:"httpsCloneBase" json:"httpsCloneBase"`
	SSHHost        string `yaml:"sshHost" json:"sshHost"`
	SSHPort        string `yaml:"sshPort" json:"sshPort"`
	ProjectID      string `yaml:"projectId" json:"projectId"`
}

// Empty reports whether no endpoint field was declared.
func (e Endpoints) Empty() bool { return e == Endpoints{} }

// CloneBinding returns the HTTPS clone prefix host, port, and path segments.
func (e Endpoints) CloneBinding() (string, string, []string, error) {
	host, port, path, err := parseEndpointURL(e.HTTPSCloneBase, false)
	if err != nil {
		return "", "", nil, err
	}
	if path == "" {
		return host, port, nil, nil
	}
	return host, port, strings.Split(strings.TrimPrefix(path, "/"), "/"), nil
}

// HasEndpoints reports whether an explicit endpoints group is declared.
func (c Config) HasEndpoints() bool { return !c.Endpoints.Empty() }

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
	endpoints := &c.Endpoints
	return map[string]*string{
		"project.id":          &c.Project.ID,
		"repository.provider": &c.Repository.Provider, "repository.host": &c.Repository.Host,
		"repository.path": &c.Repository.Path, "repository.account": &c.Repository.Account,
		"repository.credentialRef": &c.Repository.CredentialRef,
		"runtime.provider":         &c.Runtime.Provider, "runtime.account": &c.Runtime.Account,
		"runtime.credentialRef": &c.Runtime.CredentialRef, "runtime.contractRef": &c.Runtime.ContractRef,
		"billing.contract": &c.Billing.Contract, "billing.overage": &c.Billing.Overage,
		"billing.autoRecharge": &c.Billing.AutoRecharge, "billing.paidFallback": &c.Billing.PaidFallback,
		"runtime.acpCommand": &c.Runtime.ACPCommand, "runtime.acpCommandHash": &c.Runtime.ACPCommandHash,
		"runtime.acpConfig": &c.Runtime.ACPConfig, "runtime.acpConfigHash": &c.Runtime.ACPConfigHash,
		"execution.requester": &c.Execution.Requester, "execution.worker": &c.Execution.Worker,
		"execution.approvalRef":    &c.Execution.ApprovalRef,
		"execution.budgetGuidance": &c.Execution.BudgetGuidance,
		"endpoints.apiBase":        &endpoints.APIBase, "endpoints.webBase": &endpoints.WebBase,
		"endpoints.httpsCloneBase": &endpoints.HTTPSCloneBase, "endpoints.sshHost": &endpoints.SSHHost,
		"endpoints.sshPort": &endpoints.SSHPort, "endpoints.projectId": &endpoints.ProjectID,
	}
}

// MarshalJSON omits empty optional blocks so legacy selections and
// snapshots keep their existing shape.
func (c Config) MarshalJSON() ([]byte, error) {
	type configJSON Config
	shadow := struct {
		configJSON
		Endpoints *Endpoints            `json:"endpoints,omitempty"`
		Billing   *core.BillingSettings `json:"billing,omitempty"`
	}{configJSON: configJSON(c)}
	if !c.Endpoints.Empty() {
		shadow.Endpoints = &c.Endpoints
	}
	if c.Billing != (core.BillingSettings{}) {
		shadow.Billing = &c.Billing
	}
	return json.Marshal(shadow)
}

func endpointFields(c Config) map[string]string {
	if c.Endpoints.Empty() {
		return map[string]string{}
	}
	return map[string]string{
		"endpoints.apiBase": c.Endpoints.APIBase, "endpoints.webBase": c.Endpoints.WebBase,
		"endpoints.httpsCloneBase": c.Endpoints.HTTPSCloneBase, "endpoints.sshHost": c.Endpoints.SSHHost,
		"endpoints.sshPort": c.Endpoints.SSHPort, "endpoints.projectId": c.Endpoints.ProjectID,
	}
}

func (c Config) validate(required bool) error {
	if c.Version != 1 {
		return fmt.Errorf("configuration requires version 1")
	}
	for name, value := range c.fields() {
		optional := name == "execution.budgetGuidance" || strings.HasPrefix(name, "billing.") || strings.HasPrefix(name, "runtime.acp") ||
			strings.HasPrefix(name, "endpoints.")
		if required && *value == "" && !optional || strings.ContainsAny(*value, " \t\r\n") {
			return fmt.Errorf("%s requires a nonempty identifier without whitespace", name)
		}
		if c.Execution.BudgetGuidance != "" && c.Execution.BudgetGuidance != "off" &&
			c.Execution.BudgetGuidance != "flow" && c.Execution.BudgetGuidance != "account" &&
			c.Execution.BudgetGuidance != "provider" {
			return fmt.Errorf("execution.budgetGuidance requires off, flow, account, or provider")
		}
	}
	settings, err := c.Billing.Resolve()
	if err != nil {
		return err
	}
	if c.Execution.BudgetGuidance == "provider" && !settings.ProviderStops() {
		return fmt.Errorf("provider guidance requires declared billing conditions that exclude extra charges")
	}
	if err := c.validateEndpoints(required); err != nil {
		return err
	}
	switch c.Repository.Provider {
	case "", "github", "gitlab", "bitbucket":
	default:
		return fmt.Errorf("repository.provider requires github, gitlab, or bitbucket")
	}
	if c.Runtime.Provider != "" && c.Runtime.Provider != "copilot" && c.Runtime.Provider != "opencode" {
		return fmt.Errorf("runtime.provider requires copilot or opencode")
	}
	if c.Runtime.Provider == "copilot" && (c.Runtime.ACPCommand != "" || c.Runtime.ACPCommandHash != "" ||
		c.Runtime.ACPConfig != "" || c.Runtime.ACPConfigHash != "") {
		return fmt.Errorf("runtime ACP settings require the opencode provider")
	}
	if c.Runtime.Provider == "opencode" && required {
		if c.Runtime.Account != "opencode-managed" || c.Runtime.CredentialRef != "opencode-managed" {
			return fmt.Errorf("OpenCode manages its own authentication; runtime account and credentialRef require opencode-managed")
		}
		if !filepath.IsAbs(c.Runtime.ACPCommand) || !filepath.IsAbs(c.Runtime.ACPConfig) ||
			c.Runtime.ACPCommandHash != "" && !validAssetHash(c.Runtime.ACPCommandHash) ||
			!validAssetHash(c.Runtime.ACPConfigHash) {
			return fmt.Errorf("OpenCode requires absolute acpCommand/acpConfig paths, an approved acpConfig SHA256 hash, and a valid acpCommandHash when pinning the executable")
		}
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

func validAssetHash(hash string) bool {
	if len(hash) != 71 || !strings.HasPrefix(hash, "sha256:") {
		return false
	}
	for _, char := range strings.TrimPrefix(hash, "sha256:") {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

// validateEndpoints enforces the endpoint contract. A layer may declare part of
// the group because layers merge field by field, but a fully declared group,
// and every required group, must satisfy every rule.
func (c Config) validateEndpoints(required bool) error {
	values := endpointFields(c)
	if len(values) == 0 {
		return nil
	}
	declared := 0
	for _, value := range values {
		if value != "" {
			declared++
		}
	}
	if declared == 0 {
		return nil
	}
	if declared < len(values) {
		if !required {
			return nil
		}
		return fmt.Errorf("REP-002 endpoints require apiBase, webBase, httpsCloneBase, sshHost, sshPort, and projectId together")
	}
	apiHost, apiPort, apiPath, err := parseEndpointURL(values["endpoints.apiBase"], true)
	if err != nil {
		return fmt.Errorf("REP-002 endpoints.apiBase %w", err)
	}
	webHost, webPort, _, err := parseEndpointURL(values["endpoints.webBase"], false)
	if err != nil {
		return fmt.Errorf("REP-002 endpoints.webBase %w", err)
	}
	cloneHost, clonePort, _, err := parseEndpointURL(values["endpoints.httpsCloneBase"], false)
	if err != nil {
		return fmt.Errorf("REP-002 endpoints.httpsCloneBase %w", err)
	}
	sshHost := values["endpoints.sshHost"]
	if parsed, err := url.Parse("https://" + sshHost); err != nil || parsed.Hostname() == "" ||
		parsed.Host != sshHost || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" ||
		parsed.Fragment != "" || strings.ContainsAny(sshHost, "\\?#") {
		return fmt.Errorf("REP-002 endpoints.sshHost requires a hostname without a port, scheme, or path")
	}
	if !strings.EqualFold(apiHost, webHost) || !strings.EqualFold(apiHost, cloneHost) || !strings.EqualFold(apiHost, sshHost) {
		return fmt.Errorf("REP-002 endpoints require one hostname across apiBase, webBase, httpsCloneBase, and sshHost")
	}
	if apiPort != webPort || apiPort != clonePort {
		return fmt.Errorf("REP-002 endpoints require one port across apiBase, webBase, and httpsCloneBase")
	}
	if !decimal(values["endpoints.sshPort"], 1, 65535) {
		return fmt.Errorf("REP-002 endpoints.sshPort requires a decimal TCP port between 1 and 65535")
	}
	if !decimal(values["endpoints.projectId"], 1, 0) {
		return fmt.Errorf("REP-002 endpoints.projectId requires a positive decimal project ID")
	}
	if !strings.HasSuffix(apiPath, "/api/v4") {
		return fmt.Errorf("REP-002 endpoints.apiBase requires an API path ending in /api/v4")
	}
	return nil
}

// parseEndpointURL accepts an https URL with canonical path segments and no
// credentials, query, or fragment, and returns its host, port, and path.
func parseEndpointURL(value string, requireAPIPath bool) (string, string, string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(value, "\\?#%") {
		return "", "", "", fmt.Errorf("requires an https URL without credentials, query, fragment, or percent-encoding")
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if strings.Contains(path, "//") {
		return "", "", "", fmt.Errorf("requires canonical path segments")
	}
	if path != "" {
		for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			if segment == "" || segment == "." || segment == ".." {
				return "", "", "", fmt.Errorf("requires canonical path segments")
			}
		}
	}
	if requireAPIPath && path == "" {
		return "", "", "", fmt.Errorf("requires an API path ending in /api/v4")
	}
	return parsed.Hostname(), parsed.Port(), path, nil
}

func decimal(value string, minimum, maximum int) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < minimum {
		return false
	}
	return maximum == 0 || number <= maximum
}
