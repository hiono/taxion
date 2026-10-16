package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/hiono/taxion/internal/config"
)

func documentationText(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var visible []string
	var fence byte
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if fence == 0 {
				fence = trimmed[0]
			} else if fence == trimmed[0] {
				fence = 0
			}
			continue
		}
		if fence == 0 {
			visible = append(visible, line)
		}
	}
	return strings.Join(visible, "\n")
}

func documentationAnchors(text string) map[string]bool {
	anchors := map[string]bool{}
	for _, match := range regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`).FindAllStringSubmatch(text, -1) {
		anchors[match[1]] = true
	}
	seen := map[string]int{}
	for _, match := range regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*#*\s*$`).FindAllStringSubmatch(text, -1) {
		title := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(match[1], "")
		var slug strings.Builder
		for _, char := range strings.ToLower(title) {
			if char == ' ' {
				slug.WriteByte('-')
			} else if unicode.IsLetter(char) || unicode.IsNumber(char) || char == '_' || char == '-' {
				slug.WriteRune(char)
			}
		}
		id := slug.String()
		if seen[id] != 0 {
			anchors[fmt.Sprintf("%s-%d", id, seen[id])] = true
		} else {
			anchors[id] = true
		}
		seen[id]++
	}
	return anchors
}

func TestDocumentationLocalLinksWithoutNetwork(t *testing.T) {
	root := filepath.Join("..", "..")
	documents := []string{"README.md", "CONTRIBUTING.md", "SECURITY.md", "web/README.md"}
	if err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".md" {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			documents = append(documents, relative)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	links := regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	for _, document := range documents {
		source := filepath.Join(root, document)
		for _, match := range links.FindAllStringSubmatch(documentationText(t, source), -1) {
			target, err := url.Parse(match[1])
			if err != nil {
				t.Errorf("%s: invalid link %q: %v", document, match[1], err)
				continue
			}
			if target.Scheme != "" || target.Host != "" {
				continue
			}
			destination := source
			if target.Path != "" {
				destination = filepath.Join(filepath.Dir(source), target.Path)
			}
			if _, err := os.Stat(destination); err != nil {
				t.Errorf("%s: link %q: %v", document, match[1], err)
				continue
			}
			if target.Fragment != "" && filepath.Ext(destination) == ".md" &&
				!documentationAnchors(documentationText(t, destination))[target.Fragment] {
				t.Errorf("%s: link %q has no target heading or anchor", document, match[1])
			}
		}
	}
}

func TestDocumentationAnchorsPreserveJapaneseAndDuplicateHeadings(t *testing.T) {
	anchors := documentationAnchors("# 設定と実行前確認\n## CLI `--json`\n## CLI `--json`\n<a id=\"custom\"></a>")
	for _, id := range []string{"設定と実行前確認", "cli---json", "cli---json-1", "custom"} {
		if !anchors[id] {
			t.Errorf("missing heading anchor %q", id)
		}
	}
}

func TestConfigurationGuideCoversEveryFieldAndOverride(t *testing.T) {
	body, err := os.ReadFile("../../docs/userguide/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	requireRow := func(name string) {
		t.Helper()
		if !strings.Contains(string(body), "| `"+name+"` |") {
			t.Errorf("configuration guide has no table row for %q", name)
		}
	}
	typ := reflect.TypeOf(config.Config{})
	for index := range typ.NumField() {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.Type.Kind() != reflect.Struct {
			requireRow(name)
			continue
		}
		for nested := range field.Type.NumField() {
			requireRow(name + "." + strings.Split(field.Type.Field(nested).Tag.Get("json"), ",")[0])
		}
	}
	if _, found, err := config.EnvironmentLayer(func(variable string) (string, bool) {
		requireRow(variable)
		return "", false
	}); err != nil || found {
		t.Fatal("documentation lookup unexpectedly resolved an environment layer", err)
	}
}

func TestConfigurationGuideProvidesResolvableLoginDefaults(t *testing.T) {
	body, err := os.ReadFile("../../docs/userguide/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	example := regexp.MustCompile("(?s)```yaml\\n(.*?)\\n```").FindSubmatch(body)
	if len(example) != 2 {
		t.Fatal("configuration guide has no complete YAML example")
	}
	selected, err := config.Load(example[1])
	if err != nil {
		t.Fatal("configuration example is invalid", err)
	}
	if selected.Repository.CredentialRef != "gh:active" || selected.Runtime.CredentialRef != "copilot:logged-in" {
		t.Fatal("initial login example uses unresolved credential labels")
	}
	for _, command := range []string{"gh auth login", "gh auth status", "copilot login"} {
		if !strings.Contains(string(body), command) {
			t.Errorf("configuration guide omits login preparation %q", command)
		}
	}
}

func TestCLIReferenceCoversLiteralOptionNames(t *testing.T) {
	reference, err := os.ReadFile("../../docs/userguide/cli-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	declarations := []*regexp.Regexp{
		regexp.MustCompile(`\.(?:String|Bool|Int|Int64|Float64|Duration)\("([^"]+)"`),
		regexp.MustCompile(`\.(?:StringVar|BoolVar|IntVar|Int64Var|Float64Var|DurationVar|Var)\([^,]+,\s*"([^"]+)"`),
	}
	options := map[string]string{}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range declarations {
			for _, match := range declaration.FindAllSubmatch(body, -1) {
				option := string(match[1])
				options[option] = source
			}
		}
	}
	for option, source := range options {
		if !regexp.MustCompile(`--` + regexp.QuoteMeta(option) + `(?:[^a-zA-Z0-9-]|$)`).Match(reference) {
			t.Errorf("%s: CLI reference omits --%s", source, option)
		}
	}
	for _, detail := range []string{
		"Task Graph の初期化が必要",
		"`INDEPENDENT` の関連先は別 Task の Dispatch",
		"`--scanner PATH` は `runtime commit` または `--managed-commit`",
		"`--base SHA`・`--head SHA` は40桁または64桁の完全な16進 SHA を指定する必須 option",
		"Dispatch を終了する遷移では `--result-file JSON` が必須",
		"実行する `/ask`・`/plan` には正の `--question-timeout DURATION`",
		"非空で512 byte以内の検索語",
	} {
		if !strings.Contains(string(reference), detail) {
			t.Errorf("CLI reference omits option constraint %q", detail)
		}
	}
	for _, line := range strings.Split(string(reference), "\n") {
		if strings.HasPrefix(line, "| `conversation search` |") {
			if !strings.Contains(line, "`--question TEXT`") || !strings.Contains(line, "`--query TEXT`") {
				t.Fatal("CLI reference must document both conversation search query flags")
			}
			return
		}
	}
	t.Fatal("CLI reference omits the conversation search command")
}
