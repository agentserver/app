package protoconv

import (
	"encoding/json"
	"testing"
)

func TestChatRequestFromResponses(t *testing.T) {
	resp := map[string]any{
		"model":        "deepseek-v4-pro",
		"instructions": "be brief",
		"input":        "hello",
		"tools": []any{map[string]any{
			"type": "function", "name": "run", "description": "run it",
			"parameters": map[string]any{"type": "object"},
		}},
		"stream": true,
	}
	body, _ := json.Marshal(resp)

	gotBody, err := ChatRequestFromResponses(body)
	if err != nil {
		t.Fatalf("ChatRequestFromResponses: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("unmarshal converted: %v", err)
	}

	if got["model"] != "deepseek-v4-pro" {
		t.Errorf("model = %v, want deepseek-v4-pro", got["model"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) == 0 || msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("expected first message to be system from instructions, got %v", msgs)
	}
	if got["stream"] != true {
		t.Errorf("stream not forwarded: %v", got["stream"])
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "run" {
		t.Errorf("tool function name = %v, want run", fn["name"])
	}
}

func TestChatRequestMapsItems(t *testing.T) {
	resp := map[string]any{
		"model": "deepseek-v4-pro",
		"input": []any{
			map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
			map[string]any{"type": "function_call", "name": "run", "arguments": "{}", "call_id": "c1"},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": "ok"},
		},
	}
	body, _ := json.Marshal(resp)
	gotBody, err := ChatRequestFromResponses(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(gotBody, &got)

	if got.Messages[0]["role"] != "user" {
		t.Errorf("msg0 role = %v, want user", got.Messages[0]["role"])
	}
	if _, ok := got.Messages[0]["content"].(string); !ok {
		t.Errorf("msg0 content should be flattened string, got %T", got.Messages[0]["content"])
	}
	var asst map[string]any
	for _, m := range got.Messages {
		if m["role"] == "assistant" {
			asst = m
		}
	}
	if asst == nil {
		t.Fatalf("no assistant message produced from function_call")
	}
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "c1" || tc["type"] != "function" {
		t.Errorf("tool_call = %v, want id c1 type function", tc)
	}
	var tool map[string]any
	for _, m := range got.Messages {
		if m["role"] == "tool" {
			tool = m
		}
	}
	if tool == nil || tool["tool_call_id"] != "c1" {
		t.Errorf("missing tool message for c1, got %v", tool)
	}
}
