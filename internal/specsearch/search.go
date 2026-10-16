// Package specsearch searches exact approved local source snapshots.
package specsearch

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/tools"
)

type Document struct {
	Asset      tools.Asset `json:"asset"`
	Title      string      `json:"title"`
	Source     string      `json:"source"`
	Version    string      `json:"version"`
	Access     string      `json:"access"`
	MessageIDs []string    `json:"messageIds,omitempty"`
}

type Approval struct {
	Actor       string `json:"actor"`
	Source      string `json:"source"`
	Hash        string `json:"hash"`
	Copilot     bool   `json:"copilot"`
	OpenCode    bool   `json:"opencode,omitempty"`
	Publication bool   `json:"publication"`
}

type Scope struct {
	Documents         []Document   `json:"documents"`
	PDFPython         *tools.Asset `json:"pdfPython,omitempty"`
	PDFLibrary        *tools.Asset `json:"pdfLibrary,omitempty"`
	EvidenceDirectory string       `json:"evidenceDirectory"`
	Approval          *Approval    `json:"approval,omitempty"`
}

type Result struct {
	ScopeHash      string               `json:"scopeHash"`
	Query          string               `json:"query"`
	Specifications []core.Specification `json:"specifications"`
}

func (scope Scope) Hash() (string, error) {
	if err := (tools.Policy{EvidenceDirectory: scope.EvidenceDirectory}).CheckShape(); err != nil {
		return "", err
	}
	if len(scope.Documents) == 0 || len(scope.Documents) > 128 ||
		(scope.PDFPython == nil) != (scope.PDFLibrary == nil) {
		return "", fmt.Errorf("taxion: search scope requires 1-128 exact documents and a complete optional PDF parser binding")
	}
	paths := map[string]bool{}
	for _, document := range scope.Documents {
		if strings.TrimSpace(document.Title) == "" || strings.TrimSpace(document.Source) == "" ||
			strings.TrimSpace(document.Version) == "" ||
			document.Access != "public" && document.Access != "synthetic" && document.Access != "private" ||
			!filepath.IsAbs(document.Asset.Path) || paths[document.Asset.Path] ||
			len(document.Asset.Hash) != 71 {
			return "", fmt.Errorf("taxion: search documents require exact assets, source, version and an explicit access classification")
		}
		paths[document.Asset.Path] = true
		isConversation := strings.HasSuffix(strings.ToLower(document.Asset.Path), ".json.md")
		if isConversation {
			if len(document.MessageIDs) == 0 || len(document.MessageIDs) > 128 {
				return "", fmt.Errorf("taxion: conversation exports require 1-128 explicitly selected message IDs")
			}
			selected := map[string]bool{}
			for _, id := range document.MessageIDs {
				if strings.TrimSpace(id) == "" || len(id) > 128 || selected[id] {
					return "", fmt.Errorf("taxion: conversation message IDs must be nonempty and unique")
				}
				selected[id] = true
			}
			if document.Access != "private" {
				return "", fmt.Errorf("taxion: conversation exports must be classified as private")
			}
		} else if len(document.MessageIDs) != 0 {
			return "", fmt.Errorf("taxion: message IDs are valid only for conversation exports")
		}
		switch {
		case isConversation:
		case strings.EqualFold(filepath.Ext(document.Asset.Path), ".pdf"):
			if scope.PDFPython == nil {
				return "", fmt.Errorf("taxion: PDF search requires an approved Python interpreter and pdfminer library")
			}
		case strings.EqualFold(filepath.Ext(document.Asset.Path), ".docx"),
			strings.EqualFold(filepath.Ext(document.Asset.Path), ".pptx"),
			strings.EqualFold(filepath.Ext(document.Asset.Path), ".md"):
		default:
			return "", fmt.Errorf("taxion: local search supports Markdown, PDF, DOCX, PPTX and selected private conversation exports")
		}
	}
	scope.Approval = nil
	data, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

type section struct {
	Location string `json:"location"`
	Text     string `json:"text"`
}

func Search(ctx context.Context, scope Scope, query string) (Result, error) {
	hash, err := scope.Hash()
	if err != nil {
		return Result{}, err
	}
	if scope.Approval == nil || scope.Approval.Hash != hash ||
		strings.TrimSpace(scope.Approval.Actor) == "" || strings.TrimSpace(scope.Approval.Source) == "" {
		return Result{}, fmt.Errorf("taxion: local search requires sourced Human approval of the exact scope hash")
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 || len(query) > 512 {
		return Result{}, fmt.Errorf("taxion: search requires a nonempty query within 512 bytes")
	}
	result := Result{ScopeHash: hash, Query: query, Specifications: []core.Specification{}}
	for _, document := range scope.Documents {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if err := tools.CheckAsset(document.Asset, false); err != nil {
			return Result{}, err
		}
		input, err := os.Open(document.Asset.Path)
		if err != nil {
			return Result{}, fmt.Errorf("taxion: approved search document is unavailable")
		}
		data, readErr := io.ReadAll(io.LimitReader(input, (32<<20)+1))
		closeErr := input.Close()
		if readErr != nil || closeErr != nil || len(data) > 32<<20 {
			return Result{}, fmt.Errorf("taxion: search document must be readable within 32 MiB")
		}
		var sections []section
		switch {
		case strings.HasSuffix(strings.ToLower(document.Asset.Path), ".json.md"):
			sections, err = readConversation(data, document.MessageIDs)
		case strings.EqualFold(filepath.Ext(document.Asset.Path), ".md"):
			if !utf8.Valid(data) || bytes.ContainsRune(data, '\x00') {
				return Result{}, fmt.Errorf("taxion: Markdown source must be UTF-8 text without NUL")
			}
			line := 1
			for _, paragraph := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n\n") {
				end := line + strings.Count(paragraph, "\n")
				if text := strings.TrimSpace(paragraph); text != "" {
					if len(text) > 1<<20 {
						return Result{}, fmt.Errorf("taxion: Markdown paragraph exceeds 1 MiB; narrow the source")
					}
					sections = append(sections, section{Location: fmt.Sprintf("lines:%d-%d", line, end), Text: text})
				}
				line = end + 2
			}
		case strings.EqualFold(filepath.Ext(document.Asset.Path), ".pdf"):
			sections, err = readPDF(ctx, scope, data)
		case strings.EqualFold(filepath.Ext(document.Asset.Path), ".docx"),
			strings.EqualFold(filepath.Ext(document.Asset.Path), ".pptx"):
			sections, err = readOffice(data, strings.ToLower(filepath.Ext(document.Asset.Path)))
		default:
			err = fmt.Errorf("taxion: local search source type is unsupported")
		}
		if err != nil {
			return Result{}, err
		}
		if err := tools.CheckAsset(document.Asset, false); err != nil {
			return Result{}, err
		}
		for _, part := range sections {
			lower := strings.ToLower(part.Text)
			match := true
			for _, term := range terms {
				match = match && strings.Contains(lower, term)
			}
			if !match {
				continue
			}
			if len(result.Specifications) >= 64 {
				return Result{}, fmt.Errorf("taxion: search exceeds 64 matching excerpts; narrow the query")
			}
			id := sha256.Sum256([]byte(document.Asset.Path + "\x00" + document.Version + "\x00" + part.Location))
			spec := core.Specification{
				ID: fmt.Sprintf("local-%x", id[:12]), Title: document.Title,
				Source: document.Source, Location: part.Location, Version: document.Version,
				RetrievedAt: time.Now().UTC(), Body: part.Text,
				LocalPath: document.Asset.Path, DocumentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(data)),
				Usage: "Approved local search excerpt for the Task's acceptance criteria",
			}
			specHash, err := core.SpecificationHash(spec)
			if err != nil {
				return Result{}, err
			}
			approval := core.SpecificationApproval{
				Actor: scope.Approval.Actor, Source: scope.Approval.Source + " scope:" + hash, Hash: specHash,
			}
			spec.Ingestion = &approval
			if scope.Approval.Copilot {
				copy := approval
				spec.Copilot = &copy
			}
			if scope.Approval.OpenCode {
				copy := approval
				spec.OpenCode = &copy
			}
			if scope.Approval.Publication {
				copy := approval
				spec.Publication = &copy
			}
			result.Specifications = append(result.Specifications, spec)
		}
	}
	return result, nil
}

func readConversation(data []byte, selectedIDs []string) ([]section, error) {
	lines := strings.Split(string(data), "\n")
	start, end := -1, -1
	for index, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "````json" {
			if start != -1 {
				return nil, fmt.Errorf("taxion: conversation export has multiple JSON payloads")
			}
			start = index + 1
		} else if line == "````" && start != -1 {
			end = index
			break
		}
	}
	if start < 0 || end <= start {
		return nil, fmt.Errorf("taxion: conversation export JSON payload is missing")
	}
	var export struct {
		Messages []struct {
			Text      string `json:"text"`
			Author    string `json:"author"`
			MessageID string `json:"messageId"`
			Timestamp string `json:"timestamp"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(strings.Join(lines[start:end], "\n")), &export); err != nil ||
		len(export.Messages) == 0 {
		return nil, fmt.Errorf("taxion: conversation export messages are malformed")
	}
	wanted := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		wanted[id] = true
	}
	found := make(map[string]bool, len(selectedIDs))
	result := make([]section, 0, len(selectedIDs))
	for _, message := range export.Messages {
		if !wanted[message.MessageID] {
			continue
		}
		if found[message.MessageID] || !strings.EqualFold(message.Author, "user") ||
			strings.TrimSpace(message.Timestamp) == "" || strings.TrimSpace(message.Text) == "" ||
			len(message.Text) > 1<<20 {
			return nil, fmt.Errorf("taxion: selected conversation messages must be unique, user-authored, timestamped and bounded")
		}
		found[message.MessageID] = true
		result = append(result, section{Location: "message:" + message.MessageID, Text: message.Text})
	}
	if len(found) != len(wanted) {
		return nil, fmt.Errorf("taxion: one or more selected user messages are missing")
	}
	return result, nil
}

var slidePath = regexp.MustCompile(`^ppt/slides/slide([1-9][0-9]*)\.xml$`)

func readOffice(data []byte, extension string) ([]section, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("taxion: Office document is not a readable ZIP archive")
	}
	parts := []*zip.File{}
	seen := map[string]bool{}
	for _, file := range archive.File {
		if extension == ".docx" && file.Name == "word/document.xml" ||
			extension == ".pptx" && slidePath.MatchString(file.Name) {
			if seen[file.Name] || file.UncompressedSize64 > 1<<20 || len(parts) >= 4096 {
				return nil, fmt.Errorf("taxion: Office document contains duplicate or oversized text parts")
			}
			seen[file.Name] = true
			parts = append(parts, file)
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("taxion: Office document has no supported main text parts")
	}
	slices.SortFunc(parts, func(a, b *zip.File) int {
		first, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(a.Name, "ppt/slides/slide"), ".xml"))
		second, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(b.Name, "ppt/slides/slide"), ".xml"))
		return first - second
	})
	result := []section{}
	var total int
	for _, file := range parts {
		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("taxion: Office text part is inaccessible")
		}
		part, readErr := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		closeErr := reader.Close()
		total += len(part)
		if readErr != nil || closeErr != nil || len(part) > 1<<20 || total > 8<<20 {
			return nil, fmt.Errorf("taxion: Office extracted text exceeds its limit")
		}
		decoder := xml.NewDecoder(bytes.NewReader(part))
		var paragraph, slide strings.Builder
		paragraphNumber := 0
		textDepth := 0
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("taxion: Office text XML is malformed")
			}
			switch element := token.(type) {
			case xml.StartElement:
				switch element.Name.Local {
				case "t":
					textDepth++
				case "tab", "br":
					paragraph.WriteString(" ")
				}
			case xml.CharData:
				if textDepth > 0 {
					paragraph.Write(element)
				}
			case xml.EndElement:
				switch element.Name.Local {
				case "t":
					textDepth--
				case "p":
					paragraphNumber++
					text := strings.TrimSpace(paragraph.String())
					if text != "" {
						if extension == ".docx" {
							result = append(result, section{fmt.Sprintf("%s#paragraph:%d", file.Name, paragraphNumber), text})
						} else {
							slide.WriteString(text + "\n")
						}
					}
					paragraph.Reset()
				}
			}
		}
		if extension == ".pptx" && strings.TrimSpace(slide.String()) != "" {
			result = append(result, section{file.Name, strings.TrimSpace(slide.String())})
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("taxion: Office document has no searchable text")
	}
	return result, nil
}

const pdfScript = `
import json, os, resource, sys
resource.setrlimit(resource.RLIMIT_AS, (512 * 1024 * 1024, 512 * 1024 * 1024))
resource.setrlimit(resource.RLIMIT_CPU, (10, 10))
sys.path.insert(0, os.path.dirname(sys.argv[1]))
import pdfminer
if os.path.realpath(os.path.dirname(pdfminer.__file__)) != sys.argv[1]:
    raise RuntimeError("PDF parser differs from approved library")
from pdfminer.high_level import extract_pages
from pdfminer.layout import LTTextContainer
sections = []
size = 0
for number, page in enumerate(extract_pages(sys.argv[2]), 1):
    text = "".join(item.get_text() for item in page if isinstance(item, LTTextContainer)).strip()
    size += len(text.encode("utf-8"))
    if size > 1048576 or number > 4096:
        raise RuntimeError("PDF text exceeds limit")
    if text:
        sections.append({"location": "page:" + str(number), "text": text})
if not sections:
    raise RuntimeError("PDF requires searchable text; image-only sources require approved OCR")
print(json.dumps(sections, ensure_ascii=False))
`

func readPDF(ctx context.Context, scope Scope, data []byte) ([]section, error) {
	if scope.PDFPython == nil || scope.PDFLibrary == nil {
		return nil, fmt.Errorf("taxion: PDF parser approval is absent")
	}
	if err := tools.CheckAsset(*scope.PDFPython, false); err != nil {
		return nil, err
	}
	if err := tools.CheckAsset(*scope.PDFLibrary, true); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "taxion-pdf-")
	if err != nil {
		return nil, fmt.Errorf("taxion: create private PDF extraction directory")
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "source.pdf")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, fmt.Errorf("taxion: save private PDF extraction input")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, scope.PDFPython.Path, "-I", "-B", "-c", pdfScript, scope.PDFLibrary.Path, path)
	command.Dir = dir
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "LANG=C.UTF-8", "PYTHONDONTWRITEBYTECODE=1"}
	var stdout, stderr boundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	receipt, saveErr := (tools.Policy{EvidenceDirectory: scope.EvidenceDirectory}).Save(tools.Evidence{
		Tool: "pdfminer", Phase: "extraction", Input: struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
			Exit   int    `json:"exit"`
		}{stdout.String(), stderr.String(), parserExit(command)},
	})
	if saveErr != nil {
		return nil, saveErr
	}
	if runErr != nil {
		return nil, fmt.Errorf("taxion: approved PDF extraction failed (exit %d); private evidence %s", parserExit(command), receipt)
	}
	var result []section
	if json.Unmarshal(stdout.Bytes(), &result) != nil || len(result) == 0 {
		return nil, fmt.Errorf("taxion: PDF parser returned invalid searchable sections")
	}
	return result, nil
}

type boundedOutput struct{ bytes.Buffer }

func (buffer *boundedOutput) Write(data []byte) (int, error) {
	if buffer.Len()+len(data) > 8<<20 {
		return 0, fmt.Errorf("taxion: PDF output exceeds 8 MiB")
	}
	return buffer.Buffer.Write(data)
}

func parserExit(command *exec.Cmd) int {
	if command.ProcessState == nil {
		return -1
	}
	return command.ProcessState.ExitCode()
}
