package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/forge"
	"github.com/hiono/taxion/internal/publictext"
)

func sourceOrAudit(args []string, output io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("use -guidance, -audit-file or -audit-gitlab REQUEST.json")
	}
	data, err := os.Open(args[1])
	if err != nil {
		return err
	}
	defer data.Close()
	var request struct {
		Provider, Host, Repository, DocumentType, Credential, Output, RecordsFile string
	}
	decoder := json.NewDecoder(io.LimitReader(data, 1048576))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("one request object required")
	}
	p, err := publictext.LoadProfile(request.Provider, request.Host, request.Repository)
	if err != nil {
		return err
	}
	if args[0] == "-guidance" {
		content, digest, err := publictext.SelectGuidance(p, request.DocumentType)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]string{"guidanceDigest": digest, "profileHash": publictext.ProfileHash(p), "content": content})
	}
	if request.Provider != "gitlab" || request.Output == "" || args[0] == "-audit-gitlab" && request.Credential == "" {
		return fmt.Errorf("read-only audit requires GitLab binding, explicit credential and private output path")
	}
	protected := strings.Fields(os.Getenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256"))
	if len(protected) == 0 {
		return fmt.Errorf("read-only audit requires protected identifier policy")
	}
	if err := privateAuditOutput(request.Output); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var records []publictext.AuditRecord
	var notCollected []string
	if args[0] == "-audit-file" {
		input, err := os.Open(request.RecordsFile)
		if err != nil {
			return err
		}
		defer input.Close()
		decoder := json.NewDecoder(io.LimitReader(input, 16<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&records); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("one audit records array required")
		}
		notCollected = []string{"export completeness requires independent verification"}
	} else {
		records, err = forge.InventoryGitLabText(ctx, request.Host, request.Repository, request.Credential)
		if err != nil {
			return err
		}
		notCollected = []string{"CI summaries and external generated text require explicit exported records; job logs are not fetched"}
	}
	// Never emit private text to stdout or the public repository.
	file, err := os.OpenFile(request.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(file).Encode(struct {
		Results      []publictext.AuditResult `json:"results"`
		NotCollected []string                 `json:"notCollected"`
	}{publictext.Audit(p, records, protected), notCollected})
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func privateAuditOutput(path string) error {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("audit requires a known repository boundary")
	}
	canonical, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return err
	}
	worktrees, err := exec.Command("git", "worktree", "list", "--porcelain", "-z").Output()
	if err != nil {
		return fmt.Errorf("audit requires known public worktree boundaries")
	}
	roots := []string{canonical}
	for _, field := range strings.Split(string(worktrees), "\x00") {
		if path, ok := strings.CutPrefix(field, "worktree "); ok {
			roots = append(roots, path)
		}
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, parent)
		if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return fmt.Errorf("private audit output must be outside all public repository worktrees")
		}
	}
	return nil
}
