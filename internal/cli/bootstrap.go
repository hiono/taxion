package cli

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed agent_skill.md
var agentSkill []byte

func initAgentSkill(args []string, stdout, stderr io.Writer, version VersionInfo) int {
	flags := flag.NewFlagSet("taxion init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", ".", "Project directory for the local agent skill")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 2
	}
	info, err := os.Stat(*dir)
	if err != nil || !info.IsDir() {
		fmt.Fprintln(stderr, "Project directory is unavailable")
		return 1
	}
	executable, err := os.Executable()
	if err != nil {
		return report(stderr, err)
	}
	binary, err := os.Open(executable)
	if err != nil {
		return report(stderr, err)
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, binary)
	closeErr := binary.Close()
	if hashErr != nil {
		return report(stderr, hashErr)
	}
	if closeErr != nil {
		return report(stderr, closeErr)
	}
	binaryJSON, err := json.Marshal(executable)
	if err != nil {
		return report(stderr, err)
	}
	versionJSON, err := json.Marshal(valueOr(version.Version, "dev"))
	if err != nil {
		return report(stderr, err)
	}
	content := append([]byte(fmt.Sprintf("---\nname: taxion-local\ndescription: Operate Taxion's local collaboration CLI for an approved coding task.\n---\n\nTaxion executable (JSON string): `%s`\nExpected version (JSON string): `%s`\nExecutable SHA-256: `%x`\n\n", binaryJSON, versionJSON, hash.Sum(nil))), agentSkill...)
	path := filepath.Join(*dir, ".agents", "skills", "taxion-local", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return report(stderr, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Equal(existing, content) {
			fmt.Fprintf(stdout, "Skill already installed: %s\n", path)
			return 0
		}
		fmt.Fprintf(stderr, "Skill already exists and differs; not overwritten: %s\n", path)
		return 1
	}
	if err != nil {
		return report(stderr, err)
	}
	_, writeErr := f.Write(content)
	closeErr = f.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return report(stderr, writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return report(stderr, closeErr)
	}
	fmt.Fprintf(stdout, "Installed skill: %s\n", path)
	fmt.Fprintln(stdout, "Load taxion-local in a new agent session; existing loaded skills are unchanged.")
	return 0
}

func ready(args []string, stdout, stderr io.Writer, version VersionInfo) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: taxion ready")
		return 2
	}
	fmt.Fprintf(stdout, "Taxion %s local collaboration\n", valueOr(version.Version, "dev"))
	executable, err := os.Executable()
	if err != nil {
		return report(stderr, err)
	}
	bin := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	fmt.Fprintf(stdout, `1. %[1]s init --dir <project>  Install the taxion-local agent skill.
2. %[1]s doctor                Check local prerequisites.
3. %[1]s provider login        Log in to the dedicated Copilot profile.
4. %[1]s run --request "..." --working-directory <project> --allow-coder-tools
5. %[1]s job show <job-id>     Inspect the saved conversation.
6. Inspect actual workspace changes and validation results separately.

The Coder runs on the host without OS isolation. Requests and changes are sent to
the provider. A completed conversation does not guarantee successful work or review.
Do not automatically resend an interrupted run whose outcome is unknown.
`, bin)
	return 0
}

func doctor(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: taxion doctor")
		return 2
	}
	ok := true
	if runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "Local collaboration requires Linux")
		ok = false
	}
	for _, name := range []string{"copilot", "git", "diff"} {
		path, err := exec.LookPath(name)
		if err != nil {
			fmt.Fprintf(stderr, "%s not found in PATH\n", name)
			ok = false
		} else {
			fmt.Fprintf(stdout, "%s: %s\n", name, path)
		}
	}
	fmt.Fprintln(stdout, "Authentication, provider access and successful execution are not checked.")
	if !ok {
		return 1
	}
	return 0
}
