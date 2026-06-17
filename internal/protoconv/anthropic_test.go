package protoconv

import (
	"encoding/json"
	"testing"
)

func TestAnthropicRequestFromResponses(t *testing.T) {
	resp := map[string]any{
		"model":        "glm-5.2[1m]",
		"instructions": "be brief",
		"input": []any{
			map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
			map[string]any{"type": "function_call", "name": "run", "arguments": "{}", "call_id": "c1"},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": "ok"},
		},
		"tools": []any{map[string]any{"type": "function", "name": "run", "description": "d",
			"parameters": map[string]any{"type": "object"}}},
		"stream": true,
	}
	body, _ := json.Marshal(resp)
	gotBody, err := AnthropicRequestFromResponses(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(gotBody, &got)

	if got["model"] != "glm-5.2[1m]" {
		t.Errorf("model = %v", got["model"])
	}
	if got["system"] != "be brief" {
		t.Errorf("system = %v, want instructions", got["system"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 3 { // user, assistant(tool_use), user(tool_result)
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	u := msgs[0].(map[string]any)
	blocks, _ := u["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["type"] != "text" {
		t.Errorf("user content blocks = %v", blocks)
	}
	asst := msgs[1].(map[string]any)
	if asst["role"] != "assistant" {
		t.Errorf("msg1 role = %v", asst["role"])
	}
	tr := msgs[2].(map[string]any)
	if tr["role"] != "user" {
		t.Errorf("msg2 role = %v, want user (tool_result)", tr["role"])
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "run" {
		t.Errorf("tools = %v", tools)
	}
}
