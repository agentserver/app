package protoconv

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
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

// Regression (PR #12 review P2): omitting `stream` should not produce
// `"stream": null` on the wire.
func TestAnthropicRequest_OmitsStreamWhenSourceOmitsIt(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"model": "glm-5.2", "input": "hi"})
	got, err := AnthropicRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if v, present := m["stream"]; present {
		t.Errorf("stream should be absent when source omits it; got %#v", v)
	}
}

func TestAnthropicRequestFromResponses_StringInput(t *testing.T) {
	// Responses API permits a bare string as `input`; ensure it becomes a single user message.
	resp := map[string]any{"model": "glm-5.2", "input": "reply OK", "stream": true}
	body, _ := json.Marshal(resp)
	gotBody, err := AnthropicRequestFromResponses(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(gotBody, &got)
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	m := msgs[0].(map[string]any)
	if m["role"] != "user" {
		t.Errorf("role = %v, want user", m["role"])
	}
	blocks, _ := m["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["text"] != "reply OK" {
		t.Errorf("content = %v", blocks)
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

// TestAnthropicRequest_ReasoningToThinking verifies the Responses
// `reasoning.effort` → Anthropic `thinking:{type:"enabled",budget_tokens}`
// mapping (spec: GLM converter, "reasoning.effort → thinking mapping where
// applicable"). Codex sends `reasoning:{effort:"low"|"medium"|"high"|
// "xhigh"|"max"}` (the `ultra` config is rewritten to "max" on the wire).
func TestAnthropicRequest_ReasoningToThinking(t *testing.T) {
	// max_tokens defaults to 8192 when Codex omits max_output_tokens (it does
	// not send that field on the Responses request).
	const maxTokens = 8192
	cases := []struct {
		name       string
		reasoning  any
		wantThink  bool
		wantBudget int // ignored when wantThink is false
	}{
		// Budgets are hardcoded (not recomputed from the production formula) so
		// the test catches an arithmetic regression rather than agreeing on a
		// wrong value. Values: int(8192*frac), truncated.
		{"xhigh", map[string]any{"effort": "xhigh"}, true, 6553},   // 8192*0.80
		{"max", map[string]any{"effort": "max"}, true, 7372},       // 8192*0.90
		{"high", map[string]any{"effort": "high"}, true, 5324},     // 8192*0.65
		{"medium", map[string]any{"effort": "medium"}, true, 3686}, // 8192*0.45
		{"low", map[string]any{"effort": "low"}, true, 2048},       // 8192*0.25
		{"none disables", map[string]any{"effort": "none"}, false, 0},
		{"minimal disables", map[string]any{"effort": "minimal"}, false, 0},
		{"null reasoning", nil, false, 0},
		{"absent reasoning", nil, false, 0}, // modeled by not setting the key
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := map[string]any{"model": "glm-5.2", "input": "hi", "stream": true}
			if c.reasoning != nil {
				req["reasoning"] = c.reasoning
			}
			body, _ := json.Marshal(req)
			gotBody, err := AnthropicRequestFromResponses(body)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			var got map[string]any
			_ = json.Unmarshal(gotBody, &got)
			thinking, present := got["thinking"].(map[string]any)
			if c.wantThink {
				if !present {
					t.Fatalf("expected thinking field, got none; body=%s", gotBody)
				}
				if thinking["type"] != "enabled" {
					t.Errorf("thinking.type = %v, want enabled", thinking["type"])
				}
				budget, _ := thinking["budget_tokens"].(float64)
				if int(budget) != c.wantBudget {
					t.Errorf("budget_tokens = %v, want %d", budget, c.wantBudget)
				}
				// Anthropic invariants: budget > 1024 and strictly < max_tokens.
				if int(budget) <= 1024 {
					t.Errorf("budget_tokens = %d must be > 1024", int(budget))
				}
				if int(budget) >= maxTokens {
					t.Errorf("budget_tokens = %d must be < max_tokens (%d)", int(budget), maxTokens)
				}
			} else {
				if present {
					t.Errorf("expected no thinking field, got %v; body=%s", thinking, gotBody)
				}
			}
		})
	}
}

// TestAnthropicRequest_ThinkingBudgetClampsBelowMaxTokens ensures that when
// max_output_tokens is small (Codex can omit it, defaulting to 8192, but a
// caller may set a smaller value), the thinking budget stays strictly below
// max_tokens and is omitted entirely when no valid budget fits.
func TestAnthropicRequest_ThinkingBudgetClampsBelowMaxTokens(t *testing.T) {
	// Small max_tokens that still admits a valid (>1024) budget.
	body, _ := json.Marshal(map[string]any{
		"model": "glm-5.2", "input": "hi", "stream": true,
		"max_output_tokens": float64(2000),
		"reasoning":         map[string]any{"effort": "max"},
	})
	gotBody, err := AnthropicRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(gotBody, &got)
	thinking, ok := got["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking field for max effort with small max_tokens; body=%s", gotBody)
	}
	budget, _ := thinking["budget_tokens"].(float64)
	if int(budget) >= 2000 {
		t.Errorf("budget_tokens = %d must be < max_tokens 2000", int(budget))
	}
	if int(budget) <= 1024 {
		t.Errorf("budget_tokens = %d must be > 1024", int(budget))
	}

	// Tiny max_tokens: no valid budget fits, thinking must be omitted.
	body, _ = json.Marshal(map[string]any{
		"model": "glm-5.2", "input": "hi", "stream": true,
		"max_output_tokens": float64(1024),
		"reasoning":         map[string]any{"effort": "max"},
	})
	gotBody, err = AnthropicRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	// Use a fresh map: json.Unmarshal into an existing map does not delete keys
	// absent from the new payload, so the prior thinking field would leak.
	var got2 map[string]any
	_ = json.Unmarshal(gotBody, &got2)
	if _, present := got2["thinking"]; present {
		t.Errorf("expected thinking omitted when max_tokens too small; body=%s", gotBody)
	}
}

// TestAnthropicResponse_CapturesThinkingSignature verifies the non-streaming
// response path emits a `reasoning` item carrying the Anthropic thinking
// block's `signature` as `encrypted_content`. This is the first half of the
// thinking round-trip: Codex echoes reasoning items back, and the request
// converter maps encrypted_content -> Anthropic `signature` so tool-use loops
// that interleave thinking are not rejected with a 400.
func TestAnthropicResponse_CapturesThinkingSignature(t *testing.T) {
	ant := map[string]any{
		"id":    "msg_1",
		"model": "glm-5.2",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "let me plan", "signature": "sig-abc"},
			map[string]any{"type": "text", "text": "answer"},
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
	output, _ := got["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output len = %d, want 2 (reasoning + message)", len(output))
	}
	r := output[0].(map[string]any)
	if r["type"] != "reasoning" {
		t.Errorf("output[0] type = %v, want reasoning", r["type"])
	}
	if r["encrypted_content"] != "sig-abc" {
		t.Errorf("encrypted_content = %v, want sig-abc", r["encrypted_content"])
	}
	content, _ := r["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "let me plan" {
		t.Errorf("reasoning content = %v, want reasoning_text 'let me plan'", content)
	}
}

// TestAnthropicStream_CapturesThinkingSignature verifies the streaming path
// accumulates signature_delta (and thinking_delta) and emits a reasoning
// output item with the full signature as encrypted_content at block stop.
func TestAnthropicStream_CapturesThinkingSignature(t *testing.T) {
	const sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"glm-5.2\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-part-1\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-part-2\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rec := httptest.NewRecorder()
	if err := WriteAnthropicStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"encrypted_content":"sig-part-1sig-part-2"`) {
		t.Errorf("missing concatenated signature in reasoning item;\n%s", body)
	}
	if !strings.Contains(body, `"reasoning_text"`) || !strings.Contains(body, `"plan"`) {
		t.Errorf("missing reasoning_text 'plan';\n%s", body)
	}
}

// TestAnthropicRequest_EchoesReasoningAsThinkingBlock verifies the request
// converter replays a prior reasoning input item as an Anthropic thinking
// block with signature = encrypted_content. This is the second half of the
// round-trip that keeps tool-use + thinking loops from being 400-rejected.
func TestAnthropicRequest_EchoesReasoningAsThinkingBlock(t *testing.T) {
	resp := map[string]any{
		"model":        "glm-5.2",
		"instructions": "be brief",
		"input": []any{
			map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
			// Prior assistant reasoning echoed by Codex (include=reasoning.encrypted_content).
			map[string]any{
				"type": "reasoning", "encrypted_content": "sig-xyz",
				"content": []any{map[string]any{"type": "reasoning_text", "text": "pondered"}},
			},
			// Prior assistant tool call that this reasoning accompanied.
			map[string]any{"type": "function_call", "name": "run", "arguments": "{}", "call_id": "c1"},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": "ok"},
		},
		"reasoning": map[string]any{"effort": "xhigh"},
		"stream":    true,
	}
	body, _ := json.Marshal(resp)
	gotBody, err := AnthropicRequestFromResponses(body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(gotBody, &got)
	msgs, _ := got["messages"].([]any)
	if len(msgs) < 2 {
		t.Fatalf("messages len = %d, want >= 2", len(msgs))
	}
	// messages[0] = user; messages[1] = assistant(thinking block + tool_use)
	asst, ok := msgs[1].(map[string]any)
	if !ok || asst["role"] != "assistant" {
		t.Fatalf("messages[1] not assistant: %v", msgs[1])
	}
	content, _ := asst["content"].([]any)
	var foundThinking, foundToolUse bool
	for _, c := range content {
		cm, _ := c.(map[string]any)
		if cm["type"] == "thinking" {
			foundThinking = true
			if cm["signature"] != "sig-xyz" {
				t.Errorf("thinking signature = %v, want sig-xyz", cm["signature"])
			}
			if cm["thinking"] != "pondered" {
				t.Errorf("thinking text = %v, want pondered", cm["thinking"])
			}
		}
		if cm["type"] == "tool_use" {
			foundToolUse = true
		}
	}
	if !foundThinking {
		t.Errorf("missing thinking block in assistant message; content=%v", content)
	}
	if !foundToolUse {
		t.Errorf("missing tool_use block in assistant message; content=%v", content)
	}
}
