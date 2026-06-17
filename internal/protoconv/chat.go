package protoconv

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ChatUpstreamPath is where Chat Completions requests go on the gateway.
const ChatUpstreamPath = "/v1/chat/completions"

// ChatRequestFromResponses translates a Codex Responses request body to an
// OpenAI Chat Completions request body.
func ChatRequestFromResponses(respBody []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(respBody, &root); err != nil {
		return nil, fmt.Errorf("protoconv: parse responses body: %w", err)
	}

	out := map[string]any{
		"model":  root["model"],
		"stream": root["stream"],
	}
	if r, ok := root["reasoning"]; ok {
		out["reasoning"] = r
	}

	messages := []any{}
	if instr, _ := root["instructions"].(string); strings.TrimSpace(instr) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": instr})
	}

	switch in := root["input"].(type) {
	case string:
		if strings.TrimSpace(in) != "" {
			messages = append(messages, map[string]any{"role": "user", "content": in})
		}
	case []any:
		for _, item := range in {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "message":
				role, _ := m["role"].(string)
				if role == "" {
					role = "user"
				}
				messages = append(messages, map[string]any{
					"role":    role,
					"content": flattenResponsesContent(m["content"]),
				})
			case "function_call":
				name, _ := m["name"].(string)
				args, _ := m["arguments"].(string)
				callID, _ := m["call_id"].(string)
				messages = append(messages, map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": callID, "type": "function",
						"function": map[string]any{"name": name, "arguments": args},
					}},
				})
			case "function_call_output":
				callID, _ := m["call_id"].(string)
				output, _ := m["output"].(string)
				messages = append(messages, map[string]any{
					"role": "tool", "tool_call_id": callID, "content": output,
				})
			case "reasoning":
				// v1: dropped. parity is a follow-up.
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
			conv = append(conv, map[string]any{"type": "function", "function": map[string]any{
				"name":        tm["name"],
				"description": tm["description"],
				"parameters":  tm["parameters"],
			}})
		}
		out["tools"] = conv
	}

	out["messages"] = messages
	return json.Marshal(out)
}

// flattenResponsesContent turns a Responses content (string or array of
// {type,text} parts) into a single string for Chat Completions.
func flattenResponsesContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, part := range v {
			if pm, ok := part.(map[string]any); ok {
				if text, _ := pm["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}
