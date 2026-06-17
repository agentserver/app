package protoconv

import (
	"encoding/json"
	"fmt"
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
				output, _ := m["output"].(string)
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
