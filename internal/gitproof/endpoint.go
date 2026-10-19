package gitproof

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
)

// EndpointsFromSelection returns the endpoints group recorded in a dispatch
// selection, or nil when the dispatch used the legacy repository fields.
func EndpointsFromSelection(configuration *core.DispatchConfiguration) *config.Endpoints {
	if configuration == nil {
		return nil
	}
	var selected struct {
		Endpoints *config.Endpoints `json:"endpoints"`
	}
	if json.Unmarshal(configuration.Selection, &selected) != nil ||
		selected.Endpoints == nil || selected.Endpoints.Empty() {
		return nil
	}
	return selected.Endpoints
}

// Remote separates the transport identity of a Git remote from the project it
// names. The path is canonical: no leading or trailing slash, no .git suffix,
// canonical segments only.
type Remote struct {
	Scheme string
	Host   string
	Port   string
	Path   string
}

// ParseRemote classifies a Git remote as an explicit https or ssh URL, or as an
// scp-like shorthand. It refuses addresses whose project path cannot be
// separated from a port, that carry credentials, or that use encoded or
// non-canonical path segments.
func ParseRemote(raw string) (Remote, error) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return Remote{}, fmt.Errorf("REP-003 Git remote requires a single nonempty address")
	}
	if strings.Contains(value, "%") {
		return Remote{}, fmt.Errorf("REP-003 Git remote path cannot be percent-encoded")
	}
	var remote Remote
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return Remote{}, fmt.Errorf("REP-003 Git remote cannot be parsed")
		}
		if parsed.User != nil && (parsed.Scheme != "ssh" || parsed.User.Username() != "git" ||
			hasPassword(parsed.User)) {
			return Remote{}, fmt.Errorf("REP-003 Git remote cannot carry credentials")
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return Remote{}, fmt.Errorf("REP-003 Git remote cannot carry a query or fragment")
		}
		switch parsed.Scheme {
		case "https", "ssh":
			remote.Scheme = parsed.Scheme
		default:
			return Remote{}, fmt.Errorf("REP-003 Git remote requires an https or ssh scheme")
		}
		remote.Host = parsed.Hostname()
		remote.Port = parsed.Port()
		remote.Path = parsed.Path
		if remote.Host == "" {
			return Remote{}, fmt.Errorf("REP-003 Git remote requires a host")
		}
	} else {
		colon := strings.IndexByte(value, ':')
		if colon < 0 {
			return Remote{}, fmt.Errorf("REP-003 Git remote requires an https or ssh address")
		}
		location, path := value[:colon], value[colon+1:]
		if strings.Count(location, "@") > 1 {
			return Remote{}, fmt.Errorf("REP-003 Git remote cannot carry credentials")
		}
		host := location
		if at := strings.IndexByte(location, '@'); at >= 0 {
			host = location[at+1:]
		}
		if host == "" || strings.ContainsAny(host, "/\\") {
			return Remote{}, fmt.Errorf("REP-003 Git remote requires a host")
		}
		remote = Remote{Scheme: "scp", Host: host, Path: path}
	}
	segments, err := canonicalSegments(remote.Path)
	if err != nil {
		return Remote{}, err
	}
	if remote.Scheme == "scp" && digitsOnly(segments[0]) {
		return Remote{}, fmt.Errorf("REP-003 Git remote is ambiguous: an scp-like path cannot start with a port number; use an ssh URL")
	}
	remote.Path = strings.Join(segments, "/")
	return remote, nil
}

// Redacted returns the remote without credentials for diagnostics.
func (r Remote) Redacted() string {
	switch r.Scheme {
	case "scp":
		return "scp-like " + r.Host + ":" + r.Path
	default:
		if r.Port != "" {
			return r.Scheme + "://" + r.Host + ":" + r.Port + "/" + r.Path
		}
		return r.Scheme + "://" + r.Host + "/" + r.Path
	}
}

// VerifyEndpointsRemote binds a remote to an approved project through an
// explicit endpoints group. It reports the failing stage: the remote shape
// (REP-003), the project path (REP-004), or the clone prefix, host, and port
// (REP-005).
func VerifyEndpointsRemote(endpoints config.Endpoints, repository, remote string) error {
	parsed, err := ParseRemote(remote)
	if err != nil {
		return err
	}
	cloneHost, clonePort, cloneSegments, err := endpoints.CloneBinding()
	if err != nil {
		return fmt.Errorf("REP-002 endpoints.httpsCloneBase %w", err)
	}
	if !strings.EqualFold(parsed.Host, endpoints.SSHHost) || !strings.EqualFold(parsed.Host, cloneHost) {
		return fmt.Errorf("REP-005 Git origin host %q differs from the approved host %q", parsed.Host, endpoints.SSHHost)
	}
	identity := parsed.Path
	switch parsed.Scheme {
	case "https":
		if effectivePort(parsed.Port, "443") != effectivePort(clonePort, "443") {
			return fmt.Errorf("REP-005 Git origin port differs from the approved HTTPS clone port")
		}
		identity, err = trimClonePrefix(parsed.Path, cloneSegments)
		if err != nil {
			return err
		}
	case "ssh":
		if effectivePort(parsed.Port, "22") != endpoints.SSHPort {
			return fmt.Errorf("REP-005 Git origin SSH port %q differs from the approved port %q", effectivePort(parsed.Port, "22"), endpoints.SSHPort)
		}
	case "scp":
		if effectivePort(parsed.Port, "22") != endpoints.SSHPort {
			return fmt.Errorf("REP-005 Git origin port differs from the approved SSH port")
		}
	}
	if identity != repository {
		return fmt.Errorf("REP-004 Git repository path %q differs from the approved path %q", identity, repository)
	}
	return nil
}

func trimClonePrefix(path string, prefix []string) (string, error) {
	segments := strings.Split(path, "/")
	if len(segments) < len(prefix) {
		return "", fmt.Errorf("REP-005 Git origin lacks the approved HTTPS clone prefix")
	}
	for index, segment := range prefix {
		if segments[index] != segment {
			return "", fmt.Errorf("REP-005 Git origin lacks the approved HTTPS clone prefix")
		}
	}
	remainder := segments[len(prefix):]
	if len(remainder) < 2 {
		return "", fmt.Errorf("REP-005 Git origin lacks a project path after the approved clone prefix")
	}
	return strings.Join(remainder, "/"), nil
}

func canonicalSegments(path string) ([]string, error) {
	trimmed := strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if trimmed == "" {
		return nil, fmt.Errorf("REP-003 Git remote requires a project path")
	}
	if strings.Contains(trimmed, "\\") {
		return nil, fmt.Errorf("REP-003 Git remote path cannot contain a backslash")
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, fmt.Errorf("REP-003 Git remote requires canonical path segments")
		}
	}
	if len(segments) < 2 {
		return nil, fmt.Errorf("REP-003 Git remote requires a namespace and repository name")
	}
	return segments, nil
}

func effectivePort(port, fallback string) string {
	if port == "" {
		return fallback
	}
	return port
}

func hasPassword(user *url.Userinfo) bool {
	_, set := user.Password()
	return set
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
