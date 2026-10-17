package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestBashCompletionOfflineCandidatesAndSyntax(t *testing.T) {
	for _, test := range []struct {
		words string
		index string
		want  string
		omit  string
	}{
		{"taxion do", "1", "doctor", "runtime"},
		{"taxion doctor ''", "2", "fix", "--set"},
		{"taxion doctor fix --", "3", "--set", "--dispatch"},
		{"taxion doctor --json ''", "3", "--worktree", "fix"},
		{"taxion init --", "2", "--dry-run", "--json"},
		{"taxion workflow ''", "2", "feedback-resolve", "Explicitly"},
		{"taxion conversation intake --", "3", "--file", "--max-ai-credits"},
		{"taxion memory put --", "3", "--body", "--approve-sensitive"},
		{"taxion --store '/path with spaces/ledger.json' task ''", "4", "create", "--store"},
	} {
		script := bashCompletion + "\nCOMP_WORDS=(" + test.words + "); COMP_CWORD=" + test.index +
			"; _taxion_complete; printf '%s\\n' \"${COMPREPLY[@]}\"\n"
		command := exec.Command("bash", "--noprofile", "--norc")
		command.Env = []string{"HOME=" + t.TempDir(), "PATH="}
		command.Stdin = strings.NewReader(script)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatal(string(output), err)
		}
		candidates := "\n" + string(output)
		if !strings.Contains(candidates, "\n"+test.want+"\n") || strings.Contains(candidates, "\n"+test.omit+"\n") {
			t.Errorf("%s: candidates %s", test.words, output)
		}
	}
	command := exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(bashCompletion)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	var output bytes.Buffer
	if err := completionCmd([]string{"bash"}, &output); err != nil || output.String() != bashCompletion {
		t.Fatal(err)
	}
	if err := completionCmd([]string{"fish"}, &output); err == nil {
		t.Fatal("unsupported shell was accepted")
	}
	if err := completionCmd([]string{"bash", "--extra"}, &output); err == nil {
		t.Fatal("unknown arguments were accepted")
	}
}

func TestCompletionMatchesActualCLIHelp(t *testing.T) {
	command := exec.Command("go", "run", "./gencompletion", "--check")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("completion drift: %s: %v", output, err)
	}
}
