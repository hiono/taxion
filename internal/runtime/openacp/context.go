package openacp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/hiono/taxion/internal/core"
)

func (a *Adapter) prepareContext(permission map[string]any) (string, error) {
	if len(a.config.ContextDocuments) == 0 {
		return "", nil
	}
	if err := core.CheckContextDocuments(a.config.ContextDocuments); err != nil {
		return "", err
	}
	directory := filepath.Join(a.root, "config/taxion-context")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	read := map[string]string{"*": "deny"}
	external, _ := permission["external_directory"].(map[string]string)
	if external == nil {
		external = map[string]string{"*": "deny"}
	}
	if slices.Contains(a.config.AvailableTools, "read") || slices.Contains(a.config.AvailableTools, "view") {
		read["workspace"], read["workspace/*"] = "allow", "allow"
		if a.config.ToolPolicy != nil {
			for _, asset := range a.config.ToolPolicy.Skills {
				read["config/opencode/skills/"+filepath.Base(asset.Path)+"/*"] = "allow"
			}
		}
	}
	type entry struct {
		Document core.ContextDocument `json:"document"`
		Path     string               `json:"path"`
	}
	var index []entry
	for i, document := range a.config.ContextDocuments {
		name := fmt.Sprintf("%04d.txt", i)
		path := "/config/taxion-context/" + name
		metadata := document
		metadata.Text = ""
		var body bytes.Buffer
		body.WriteString("QUOTED SOURCE CHUNKS v1: concatenate decoded text fields without adding separators; Unicode character offsets.\n")
		encoder := json.NewEncoder(&body)
		text := []rune(document.Text)
		// Native read truncates long lines; JSON escapes can use six bytes per
		// character. Short lossless chunks remain below its 2000-character cap.
		for offset := 0; offset < len(text); {
			end := offset + min(192, len(text)-offset)
			if err := encoder.Encode(struct {
				Offset     int    `json:"offset"`
				NextOffset int    `json:"nextOffset"`
				EOF        bool   `json:"eof"`
				Text       string `json:"text"`
			}{offset, end, end == len(text), string(text[offset:end])}); err != nil {
				return "", err
			}
			offset = end
		}
		if err := os.WriteFile(filepath.Join(directory, name), body.Bytes(), 0400); err != nil {
			return "", err
		}
		// With no Git metadata, OpenCode's native project worktree is "/",
		// not its process working directory "/workspace".
		read[path[1:]] = "allow"
		index = append(index, entry{metadata, path})
	}
	external["/config/taxion-context/*"] = "allow"
	permission["read"], permission["external_directory"] = read, external
	data, err := json.Marshal(index)
	if err != nil {
		return "", err
	}
	return "\n\n" + core.SourceContextGuidance +
		"\nUse native read on these exact read-only paths with limit:8 and line pagination. Files contain JSON lines of lossless text chunks, not summaries. Concatenate decoded text fields without adding separators; preserve original line breaks inside each string. Continue through eof:true. Other /config files and unapproved workspace reads remain unavailable.\n" +
		string(data), nil
}
