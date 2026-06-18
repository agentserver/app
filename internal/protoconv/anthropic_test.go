package protoconv

import (
	"encoding/json"
	"testing"
)

func TestAnthropicRequestFromResponses(t *testing.T) {
	resp := map[string]any{
		"model":        "glm-5.2",
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

	if got["model"] != "glm-5.2" {
		t.Errorf("model = %v", got["model"])
	}
	if mt, ok := got["max_tokens"].(float64); !ok || mt != 8192 {
		t.Errorf("max_tokens = %v, want 8192 (default)", got["max_tokens"])
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

func TestAnthropicResponseToResponses(t *testing.T) {
	ant := map[string]any{
		"id":    "msg_1",
		"model": "glm-5.2",
		"content": []any{
			map[string]any{"type": "text", "text": "hello there"},
			map[string]any{"type": "tool_use", "id": "c1", "name": "run", "input": map[string]any{}},
		},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 2},
	}
	body, _ := json.Marshal(ant)
	out, err := AnthropicResponseToResponses(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["status"] != "completed" {
		t.Errorf("status = %v", got["status"])
	}
	output, _ := got["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output len = %d, want 2", len(output))
	}
	if output[0].(map[string]any)["type"] != "message" {
		t.Errorf("output[0] type = %v", output[0].(map[string]any)["type"])
	}
	if output[1].(map[string]any)["type"] != "function_call" {
		t.Errorf("output[1] type = %v", output[1].(map[string]any)["type"])
	}
}
