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

	// Anthropic Messages API requires max_tokens. Map it from the Responses
	// request's max_output_tokens when present, else default.
	maxTokens := 8192
	if mot, ok := root["max_output_tokens"].(float64); ok && mot > 0 {
		maxTokens = int(mot)
	}
	out["max_tokens"] = maxTokens

	// system: instructions + any developer/system input messages
	systemParts := []string{}
	if instr, _ := root["instructions"].(string); strings.TrimSpace(instr) != "" {
		systemParts = append(systemParts, instr)
	}

	messages := []any{}
	switch in := root["input"].(type) {
	case string:
		// Responses API permits a bare string as a single user message.
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": in}},
		})
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
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	counter := 0
	newID := func(prefix string) string { counter++; return fmt.Sprintf("%s_%d", prefix, counter) }

	var event string
	var curItemID string // id of the currently open content block; "" = none
	curToolID := ""      // if the open block is tool_use, the tool id; else ""
	curToolName := ""    // function name carried from content_block_start
	var textBuf strings.Builder
	var argsBuf strings.Builder

	// Upstream response identity, captured from message_start. Codex's
	// Responses parser rejects the stream with "missing field `id`" if these
	// are absent from response.created/response.completed.
	respID := ""
	respModel := ""
	createdEmitted := false
	emitCreated := func() {
		if createdEmitted {
			return
		}
		createdEmitted = true
		writeEvent("response.created", map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":     respID,
				"model":  respModel,
				"status": "in_progress",
			},
		})
	}

	closeCurrent := func() {
		if curItemID == "" {
			return
		}
		if curToolID != "" {
			// Codex's ResponseItem::FunctionCall requires {name, arguments, call_id}.
			writeEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":      "function_call",
					"id":        curToolID,
					"call_id":   curToolID,
					"name":      curToolName,
					"arguments": argsBuf.String(),
				},
			})
			curToolID = ""
			curToolName = ""
			argsBuf.Reset()
		} else {
			// Codex's ResponseItem::Message requires {role, content:[{type,text}]}.
			writeEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":    "message",
					"id":      curItemID,
					"role":    "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": textBuf.String()}},
				},
			})
			textBuf.Reset()
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
		case "message_start":
			if m, _ := msg["message"].(map[string]any); m != nil {
				if id, _ := m["id"].(string); id != "" {
					respID = id
				}
				if mdl, _ := m["model"].(string); mdl != "" {
					respModel = mdl
				}
			}
			emitCreated()
		case "content_block_start":
			emitCreated() // some gateways omit message_start; ensure created fires before any item

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
				curToolName = name
				writeEvent("response.output_item.added", map[string]any{
					"type": "response.output_item.added",
					"item": map[string]any{
						"type":      "function_call",
						"id":        id,
						"call_id":   id,
						"name":      name,
						"arguments": "",
					},
				})
			case "text", "":
				curItemID = newID("msg")
				writeEvent("response.output_item.added", map[string]any{
					"type": "response.output_item.added",
					"item": map[string]any{
						"type":    "message",
						"id":      curItemID,
						"role":    "assistant",
						"content": []any{},
					},
				})
			default:
				// thinking / server-tool / other deferred block types: ignore
				// without opening an output item (reasoning parity is a follow-up).
			}
		case "content_block_delta":
			delta, _ := msg["delta"].(map[string]any)
			if curItemID == "" {
				continue
			}
			switch delta["type"] {
			case "text_delta":
				if text, _ := delta["text"].(string); text != "" {
					textBuf.WriteString(text)
					writeEvent("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": curItemID, "delta": text})
				}
			case "input_json_delta":
				if pj, _ := delta["partial_json"].(string); pj != "" {
					argsBuf.WriteString(pj)
					writeEvent("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": curItemID, "delta": pj})
				}
			}
		case "content_block_stop":
			closeCurrent()
		case "message_stop":
			closeCurrent()
		}
	}
	closeCurrent()
	emitCreated() // edge case: empty stream — still emit a well-formed pair
	writeEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     respID,
			"model":  respModel,
			"status": "completed",
		},
	})
	return scanner.Err()
}
