package specsearch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// Serve is a read-only MCP endpoint for this exact locally approved corpus.
func Serve(ctx context.Context, scope Scope, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	encoder := json.NewEncoder(output)
	initialized := false
	for scanner.Scan() {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.JSONRPC != "2.0" {
			return fmt.Errorf("taxion: invalid local search MCP request")
		}
		if len(request.ID) == 0 {
			if request.Method != "notifications/initialized" || !initialized {
				return fmt.Errorf("taxion: unexpected local search MCP notification")
			}
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		var result any
		var operationErr error
		if !initialized && request.Method != "initialize" {
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID,
				"error": map[string]any{"code": -32600, "message": "taxion: local search MCP requires initialization"}}); err != nil {
				return err
			}
			continue
		}
		switch request.Method {
		case "initialize":
			initialized = true
			result = map[string]any{"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo":   map[string]any{"name": "taxion-local-specifications", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "specification_search", "description": "Search exact user-approved Markdown, PDF, DOCX, PPTX and explicitly selected user messages from private conversation exports",
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false,
					"properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 512}},
					"required":   []string{"query"}},
				"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false},
			}}}
		case "tools/call":
			var call struct {
				Name      string `json:"name"`
				Arguments struct {
					Query string `json:"query"`
				} `json:"arguments"`
			}
			operationErr = core.DecodeGraphInput(strings.NewReader(string(request.Params)), &call)
			if operationErr == nil && call.Name != "specification_search" {
				operationErr = fmt.Errorf("taxion: unknown local search tool")
			}
			if operationErr == nil {
				var found Result
				found, operationErr = Search(ctx, scope, call.Arguments.Query)
				if operationErr == nil {
					body, err := json.Marshal(found)
					operationErr = err
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(body)}}, "isError": false}
				}
			}
		default:
			operationErr = fmt.Errorf("taxion: unsupported local search MCP method")
		}
		if operationErr != nil {
			response["error"] = map[string]any{"code": -32602, "message": operationErr.Error()}
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("taxion: read local search MCP input: %w", err)
	}
	return nil
}
