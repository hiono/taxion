package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"time"
)

// CallMCP uses a fresh local connection for a single fixed approved request.
// Arguments supplied by the model are checked before starting any host process.
func (p Policy) CallMCP(ctx context.Context, name string, arguments any) (json.RawMessage, error) {
	var selected *MCPTool
	for i := range p.MCP {
		if p.MCP[i].Name == name {
			selected = &p.MCP[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("taxion: MCP tool is absent from the role's approval")
	}
	candidate, err := json.Marshal(arguments)
	if err != nil || !sameJSON(candidate, selected.Arguments) {
		return nil, fmt.Errorf("taxion: MCP arguments differ from the approved request")
	}
	if err := p.Check(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, selected.Command.Path, selected.Args...)
	command.Dir = p.EvidenceDirectory
	if err := privateDirectory(command.Dir); err != nil {
		return nil, err
	}
	// A server runs with host permissions. Only explicitly trusted commands
	// and their pinned inputs belong here; ambient credentials are withheld.
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + command.Dir, "LANG=C.UTF-8"}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("taxion: open MCP input")
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("taxion: open MCP output")
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("taxion: start approved MCP server")
	}
	defer func() {
		input.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	encoder := json.NewEncoder(input)
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var transcript limitedBuffer
	request := func(id int, method string, params any) (json.RawMessage, error) {
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			return nil, fmt.Errorf("taxion: send MCP request")
		}
		var total int
		for scanner.Scan() {
			if _, err := transcript.Write(append(slices.Clone(scanner.Bytes()), '\n')); err != nil {
				return nil, err
			}
			total += len(scanner.Bytes())
			if total > 16<<20 {
				return nil, fmt.Errorf("taxion: MCP response exceeds 16 MiB")
			}
			var response struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      *int            `json:"id"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			if json.Unmarshal(scanner.Bytes(), &response) != nil || response.JSONRPC != "2.0" {
				return nil, fmt.Errorf("taxion: invalid MCP response")
			}
			if response.ID == nil {
				continue
			}
			if *response.ID != id {
				return nil, fmt.Errorf("taxion: MCP response identity differs")
			}
			if len(response.Error) != 0 && string(response.Error) != "null" || len(response.Result) == 0 {
				return nil, fmt.Errorf("taxion: MCP server reported a protocol failure")
			}
			return response.Result, nil
		}
		return nil, fmt.Errorf("taxion: MCP response ended before the request completed")
	}
	initialized, callErr := request(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "taxion", "version": "1"},
	})
	if callErr == nil {
		var initialization struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(initialized, &initialization) != nil || initialization.ProtocolVersion != "2024-11-05" {
			callErr = fmt.Errorf("taxion: unsupported MCP protocol version")
		}
	}
	if callErr == nil {
		callErr = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	}
	var result json.RawMessage
	if callErr == nil {
		result, callErr = request(2, "tools/call", map[string]any{"name": selected.Tool, "arguments": selected.Arguments})
	}
	input.Close()
	if callErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	receipt, saveErr := p.Save(Evidence{Tool: name, Phase: "mcp", Input: struct {
		Result json.RawMessage `json:"result,omitempty"`
		Stdout string          `json:"stdout"`
		Stderr string          `json:"stderr"`
		Exit   int             `json:"exit"`
	}{result, transcript.String(), stderr.String(), exitCode(command)}})
	if saveErr != nil {
		return nil, saveErr
	}
	if callErr != nil || waitErr != nil {
		return nil, fmt.Errorf("taxion: MCP call failed; private evidence %s", receipt)
	}
	var status struct {
		IsError bool              `json:"isError"`
		Content []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(result, &status) != nil || status.Content == nil || status.IsError {
		return nil, fmt.Errorf("taxion: MCP tool returned a failure; private evidence %s", receipt)
	}
	return result, nil
}

func sameJSON(left, right []byte) bool {
	var a, b any
	decoderA := json.NewDecoder(bytes.NewReader(left))
	decoderB := json.NewDecoder(bytes.NewReader(right))
	decoderA.UseNumber()
	decoderB.UseNumber()
	if decoderA.Decode(&a) != nil || decoderB.Decode(&b) != nil {
		return false
	}
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(encodedA) == string(encodedB)
}
