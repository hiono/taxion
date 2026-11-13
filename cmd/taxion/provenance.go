package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/provenance"
)

func activeProvenanceIdentity() (provenance.Identity, error) {
	executable, err := os.Executable()
	if err != nil {
		return provenance.Identity{}, err
	}
	identity := provenance.Identity{Version: releaseVersion, Revision: releaseRevision,
		Channel: releaseChannel, OS: runtime.GOOS, Architecture: runtime.GOARCH, Executable: executable}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				identity.Dirty = true
			}
		}
	}
	return identity, nil
}

func operationPolicyPath() string {
	return filepath.Join(filepath.Dir(storePath), "operation-policy.json")
}

func verifyOperationProvenance() (provenance.Report, error) {
	data, err := provenance.Read(operationPolicyPath(), 1<<20)
	if err != nil {
		return provenance.Report{}, fmt.Errorf("explicit operation policy required before using a store: %w", err)
	}
	var policy provenance.Policy
	if err := provenance.Decode(data, &policy); err != nil {
		return provenance.Report{}, err
	}
	identity, err := activeProvenanceIdentity()
	if err != nil {
		return provenance.Report{}, err
	}
	return provenance.Verify(policy, filepath.Dir(storePath), identity)
}

func provenanceDiagnostic(err error) error {
	if outputErr := printJSON(struct {
		Code         string `json:"code"`
		Message      string `json:"message"`
		StateChanged bool   `json:"stateChanged"`
	}{"provenance-rejected", core.Redact("Operation rejected: " + err.Error()), false}); outputErr != nil {
		return outputErr
	}
	return err
}

func provenanceHelpOnly(args []string) bool {
	if len(args) < 2 || (args[len(args)-1] != "--help" && args[len(args)-1] != "-h") {
		return false
	}
	path := strings.Join(args[:len(args)-1], " ")
	if slices.Contains([]string{"git hook install", "git hook status", "git hook remove",
		"memory conflict add", "memory conflict list", "memory conflict resolve"}, path) {
		return true
	}
	for _, line := range strings.Split(usage, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		words := strings.Fields(line)
		if len(words) < 2 {
			continue
		}
		command := words[0]
		if words[1][0] >= 'a' && words[1][0] <= 'z' {
			command += " " + words[1]
		}
		if path == command {
			return true
		}
	}
	return false
}

func provenanceCmd(args []string) error {
	if len(args) == 1 && args[0] == "verify" {
		report, err := verifyOperationProvenance()
		if err != nil {
			return provenanceDiagnostic(err)
		}
		return printJSON(report)
	}
	if len(args) == 0 || args[0] != "configure" {
		return fmt.Errorf("usage: taxion provenance verify | configure --file POLICY.json")
	}
	flags := flag.NewFlagSet("provenance configure", flag.ContinueOnError)
	file := flags.String("file", "", "explicit immutable operation policy JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *file == "" || flags.NArg() != 0 {
		return fmt.Errorf("provenance configure requires --file and no positional arguments")
	}
	data, err := provenance.Read(*file, 1<<20)
	if err != nil {
		return provenanceDiagnostic(err)
	}
	var policy provenance.Policy
	if err := provenance.Decode(data, &policy); err != nil {
		return provenanceDiagnostic(err)
	}
	identity, err := activeProvenanceIdentity()
	if err != nil {
		return provenanceDiagnostic(err)
	}
	report, err := provenance.Verify(policy, filepath.Dir(storePath), identity)
	if err != nil {
		return provenanceDiagnostic(err)
	}
	canonical, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	// A mode cannot silently downgrade or repin an existing store policy.
	output, err := os.OpenFile(operationPolicyPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return provenanceDiagnostic(err)
	}
	if _, err := output.Write(append(canonical, '\n')); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return printJSON(report)
}
