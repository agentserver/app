package protoconv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AnthropicUpstreamPath is where Anthropic Messages requests go on the gateway.
const AnthropicUpstreamPath = "/v1/messages"

// AnthropicRequestFromResponses translates a Codex Responses request body to
// an Anthropic Messages request body.
func AnthropicRequestFromResponses(respBody []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(respBody, &root); err != nil {
		return nil, fmt.Errorf("protoconv: parse responses body: %w", err)
	}

	out := map[string]any{
		"model":  root["model"],
		"stream": root["stream"],
	}

	// system: instructions + any developer/system input messages
	systemParts := []string{}
	if instr, _ := root["instructions"].(string); strings.TrimSpace(instr) != "" {
		systemParts = append(systemParts, instr)
	}

	messages := []any{}
	switch in := root["input"].(type) {
	case []any:
		for _, item := range in {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "message":
				role, _ := m["role"].(string)
				switch strings.ToLower(role) {
				case "system", "developer":
					if t := flattenResponsesContent(m["content"]); t != "" {
						systemParts = append(systemParts, t)
					}
				default:
					if role == "" {
						role = "user"
					}
					messages = append(messages, map[string]any{
						"role":    role,
						"content": anthropicContentBlocks(m["content"]),
					})
				}
			case "function_call":
				name, _ := m["name"].(string)
				args, _ := m["arguments"].(string)
				callID, _ := m["call_id"].(string)
				var input any
				_ = json.Unmarshal([]byte(args), &input) // best-effort object
				if input == nil {
					input = map[string]any{}
				}
				messages = append(messages, map[string]any{
					"role": "assistant",
					"content": []any{map[string]any{
						"type": "tool_use", "id": callID, "name": name, "input": input,
					}},
				})
			case "function_call_output":
				callID, _ := m["call_id"].(string)
				output := flattenResponsesContent(m["output"])
				messages = append(messages, map[string]any{
					"role": "user",
					"content": []any{map[string]any{
						"type": "tool_result", "tool_use_id": callID, "content": output,
					}},
				})
			case "reasoning":
				// v1: dropped (parity follow-up).
			}
		}
	}

	if tools, ok := root["tools"].([]any); ok {
		conv := make([]any, 0, len(tools))
		for _, t := range tools {
			tm, ok := t.(map[string]any)
			if !ok || tm["type"] != "function" {
				continue
			}
			conv = append(conv, map[string]any{
				"name":         tm["name"],
				"description":  tm["description"],
				"input_schema": tm["parameters"],
			})
		}
		out["tools"] = conv
	}

	if len(systemParts) > 0 {
		out["system"] = strings.Join(systemParts, "\n\n")
	}
	out["messages"] = messages
	return json.Marshal(out)
}

// anthropicContentBlocks turns Responses content into Anthropic text content blocks.
func anthropicContentBlocks(content any) []any {
	switch v := content.(type) {
	case string:
		return []any{map[string]any{"type": "text", "text": v}}
	case []any:
		blocks := []any{}
		for _, part := range v {
			if pm, ok := part.(map[string]any); ok {
				if text, _ := pm["text"].(string); text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": text})
				}
			}
		}
		return blocks
	}
	return []any{map[string]any{"type": "text", "text": ""}}
}

// AnthropicResponseToResponses assembles a complete (non-streaming) Anthropic
// Messages response body into a Responses-shaped body.
func AnthropicResponseToResponses(antBody []byte) ([]byte, error) {
	var ant struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage any `json:"usage"`
	}
	if err := json.Unmarshal(antBody, &ant); err != nil {
		return nil, fmt.Errorf("protoconv: parse anthropic body: %w", err)
	}

	output := []any{}
	for _, b := range ant.Content {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				output = append(output, map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": b.Text}},
				})
			}
		case "tool_use":
			output = append(output, map[string]any{
				"type": "function_call", "name": b.Name, "call_id": b.ID,
				"arguments": string(b.Input),
			})
		}
	}

	resp := map[string]any{
		"id":     ant.ID,
		"model":  ant.Model,
		"status": "completed",
		"output": output,
		"usage":  ant.Usage,
	}
	return json.Marshal(resp)
}

// WriteAnthropicStreamAsResponses reads an Anthropic Messages SSE stream and
// writes the Responses SSE event sequence. Each content block (text or tool_use)
// gets a balanced added/done pair; deltas carry item_id.
func WriteAnthropicStreamAsResponses(r io.Reader, w http.ResponseWriter) error {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	writeEvent := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	writeEvent("response.created", map[string]any{"type": "response.created"})

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	counter := 0
	newID := func(prefix string) string { counter++; return fmt.Sprintf("%s_%d", prefix, counter) }

	var event string
	var curItemID string // id of the currently open content block; "" = none
	curToolID := ""      // if the open block is tool_use, the tool id; else ""

	closeCurrent := func() {
		if curItemID == "" {
			return
		}
		if curToolID != "" {
			writeEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "id": curToolID, "call_id": curToolID}})
			curToolID = ""
		} else {
			writeEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "id": curItemID}})
		}
		curItemID = ""
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			continue
		}
		switch event {
		case "content_block_start":
			closeCurrent()
			block, _ := msg["content_block"].(map[string]any)
			switch block["type"] {
			case "tool_use":
				id, _ := block["id"].(string)
				if id == "" {
					id = newID("fc")
				}
				name, _ := block["name"].(string)
				curItemID = id
				curToolID = id
				writeEvent("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "function_call", "id": id, "call_id": id, "name": name}})
			default: // text
				curItemID = newID("msg")
				writeEvent("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "message", "id": curItemID}})
			}
		case "content_block_delta":
			delta, _ := msg["delta"].(map[string]any)
			if curItemID == "" {
				continue
			}
			switch delta["type"] {
			case "text_delta":
				if text, _ := delta["text"].(string); text != "" {
					writeEvent("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": curItemID, "delta": text})
				}
			case "input_json_delta":
				if pj, _ := delta["partial_json"].(string); pj != "" {
					writeEvent("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": curItemID, "delta": pj})
				}
			}
		case "content_block_stop":
			closeCurrent()
		case "message_stop":
			closeCurrent()
			writeEvent("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}})
		}
	}
	return scanner.Err()
}
