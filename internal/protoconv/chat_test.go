package protoconv

import (
	"encoding/json"
	"strings"
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

// Regression (PR #12 review P4): Codex 0.142 sends parallel_tool_calls and
// tool_choice; both must reach the upstream Chat endpoint. Function tools'
// strict flag must also survive translation.
func TestChatRequest_ForwardsToolControlFields(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model":               "deepseek-v4-pro",
		"input":               "hi",
		"parallel_tool_calls": false,
		"tool_choice":         "required",
		"tools": []any{map[string]any{
			"type": "function", "name": "run", "description": "d",
			"parameters": map[string]any{"type": "object"},
			"strict":     true,
		}},
	})
	got, err := ChatRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if v, _ := m["parallel_tool_calls"].(bool); v {
		t.Errorf("parallel_tool_calls = %v, want false", v)
	}
	if v, ok := m["parallel_tool_calls"]; !ok {
		t.Errorf("parallel_tool_calls dropped, want forwarded: %#v", v)
	}
	if v, _ := m["tool_choice"].(string); v != "required" {
		t.Errorf("tool_choice = %v, want \"required\"", m["tool_choice"])
	}
	tools := m["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if v, _ := fn["strict"].(bool); !v {
		t.Errorf("function.strict = %v, want true (preserved)", v)
	}
}

func TestChatRequest_OmitsToolControlsWhenSourceOmitsThem(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model": "deepseek-v4-pro",
		"input": "hi",
		"tools": []any{map[string]any{
			"type": "function", "name": "run", "description": "d",
			"parameters": map[string]any{"type": "object"},
		}},
	})
	got, err := ChatRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if _, present := m["parallel_tool_calls"]; present {
		t.Errorf("parallel_tool_calls should be absent when source omits it")
	}
	if _, present := m["tool_choice"]; present {
		t.Errorf("tool_choice should be absent when source omits it")
	}
	tools := m["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if _, present := fn["strict"]; present {
		t.Errorf("function.strict should be absent when source omits it")
	}
}

// Regression (PR #12 review P2): omitting `stream` from the source body should
// not produce `"stream": null` (rejected by upstream Chat validators).
func TestChatRequest_OmitsStreamWhenSourceOmitsIt(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"model": "deepseek-v4-pro", "input": "hi"})
	got, err := ChatRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if v, present := m["stream"]; present {
		t.Errorf("stream should be absent when source omits it; got %#v", v)
	}
}

// Regression: Codex sends `developer` role for prompt-author instructions, but
// Chat Completions only accepts {system, assistant, user, tool, function} —
// DeepSeek rejects it with "developer is not one of [...]". Merge developer +
// inline system messages into a single leading system message (mirrors the
// Anthropic converter).
func TestChatRequestMergesDeveloperIntoSystem(t *testing.T) {
	resp := map[string]any{
		"model":        "deepseek-v4-pro",
		"instructions": "be brief",
		"input": []any{
			map[string]any{"type": "message", "role": "developer",
				"content": []any{map[string]any{"type": "input_text", "text": "use Chinese"}}},
			map[string]any{"type": "message", "role": "system",
				"content": []any{map[string]any{"type": "input_text", "text": "extra rule"}}},
			map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
		},
	}
	body, _ := json.Marshal(resp)
	gotBody, err := ChatRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(gotBody, &got)
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2 (system+user); got %+v", len(msgs), msgs)
	}
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Errorf("msg[0] role = %v, want system", sys["role"])
	}
	for _, want := range []string{"be brief", "use Chinese", "extra rule"} {
		if c, _ := sys["content"].(string); !strings.Contains(c, want) {
			t.Errorf("system content missing %q; got %q", want, c)
		}
	}
	if u := msgs[1].(map[string]any); u["role"] != "user" || u["content"] != "hi" {
		t.Errorf("msg[1] = %v, want user/hi", u)
	}
	for _, m := range msgs {
		if r := m.(map[string]any)["role"]; r == "developer" {
			t.Errorf("developer role leaked into Chat messages: %v", m)
		}
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

func TestChatResponseToResponses(t *testing.T) {
	chat := map[string]any{
		"id":    "chat_1",
		"model": "deepseek-v4-pro",
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": "hello there",
				"tool_calls": []any{map[string]any{
					"id": "c1", "type": "function",
					"function": map[string]any{"name": "run", "arguments": "{}"},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 2},
	}
	body, _ := json.Marshal(chat)

	out, err := ChatResponseToResponses(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)

	if got["model"] != "deepseek-v4-pro" {
		t.Errorf("model = %v", got["model"])
	}
	if got["status"] != "completed" {
		t.Errorf("status = %v, want completed", got["status"])
	}
	output, _ := got["output"].([]any)
	if len(output) < 2 {
		t.Fatalf("output items = %d, want >=2 (message + function_call)", len(output))
	}
	first := output[0].(map[string]any)
	if first["type"] != "message" {
		t.Errorf("first output type = %v, want message", first["type"])
	}
	var foundFn bool
	for _, it := range output {
		if m, ok := it.(map[string]any); ok && m["type"] == "function_call" {
			foundFn = true
			if m["name"] != "run" || m["call_id"] != "c1" {
				t.Errorf("function_call = %v", m)
			}
		}
	}
	if !foundFn {
		t.Errorf("no function_call item in output")
	}
}
