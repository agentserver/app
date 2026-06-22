package protoconv

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicStreamToResponses(t *testing.T) {
	const sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"glm-5.2\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	rec := httptest.NewRecorder()
	err := WriteAnthropicStreamAsResponses(strings.NewReader(sse), rec)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		"event: response.completed",
		`"hi"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, body)
		}
	}
}

// Regression: Codex Responses parser fails with
//
//	"stream disconnected before completion: failed to parse ResponseCompleted: missing field `id`"
//
// unless response.created and response.completed both carry the upstream
// response id (and model). Pull them out of Anthropic's message_start.
func TestAnthropicStreamCarriesResponseIDAndModel(t *testing.T) {
	const sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_abc123\",\"model\":\"glm-5.2\"}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rec := httptest.NewRecorder()
	if err := WriteAnthropicStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"id":"msg_abc123"`,
		`"model":"glm-5.2"`,
		`"status":"in_progress"`,
		`"status":"completed"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, body)
		}
	}
	// Both response.created and response.completed must contain id+model.
	for _, ev := range []string{"response.created", "response.completed"} {
		idx := strings.Index(body, "event: "+ev)
		if idx < 0 {
			t.Errorf("event %s missing\n%s", ev, body)
			continue
		}
		end := strings.Index(body[idx:], "\n\n")
		if end < 0 {
			t.Errorf("event %s frame truncated\n%s", ev, body)
			continue
		}
		frame := body[idx : idx+end]
		if !strings.Contains(frame, `"id":"msg_abc123"`) {
			t.Errorf("event %s missing id:\n%s", ev, frame)
		}
		if !strings.Contains(frame, `"model":"glm-5.2"`) {
			t.Errorf("event %s missing model:\n%s", ev, frame)
		}
	}
}

func TestAnthropicStreamMultiItemBalanced(t *testing.T) {
	const sse = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c1\",\"name\":\"run\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rec := httptest.NewRecorder()
	if err := WriteAnthropicStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	added, done, argWithItem := countItemEvents(body)
	if added != done {
		t.Errorf("added=%d done=%d (must balance);\n%s", added, done, body)
	}
	if added != 2 { // text + tool_use
		t.Errorf("added=%d, want 2", added)
	}
	if argWithItem < 1 {
		t.Errorf("tool arg delta missing item_id")
	}
	// frame structure: every non-blank line starts with event: or data:
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "event:") || strings.HasPrefix(l, "data:") {
			continue
		}
		t.Errorf("unexpected SSE line: %q", l)
	}
}

func TestAnthropicStreamEmitsCompletedOnTruncation(t *testing.T) {
	// Upstream closes mid-stream with no message_stop.
	const sse = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"
	rec := httptest.NewRecorder()
	if err := WriteAnthropicStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: response.completed") {
		t.Errorf("missing response.completed on truncated stream;\n%s", body)
	}
	added, done, _ := countItemEvents(body)
	if added != done {
		t.Errorf("added=%d done=%d (must balance even on truncation)", added, done)
	}
}

func TestAnthropicStreamThinkingBlockOpensNoItem(t *testing.T) {
	// A thinking block must not open a spurious empty message item.
	const sse = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"reasoning here\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	rec := httptest.NewRecorder()
	if err := WriteAnthropicStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	added, done, _ := countItemEvents(body)
	if added != 0 || done != 0 {
		t.Errorf("thinking block opened items: added=%d done=%d (want 0/0);\n%s", added, done, body)
	}
	if !strings.Contains(body, "event: response.completed") {
		t.Errorf("missing response.completed;\n%s", body)
	}
}

func TestAnthropicRequestFunctionCallOutputArray(t *testing.T) {
	resp := map[string]any{
		"model": "glm-5.2",
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "c1",
				"output": []any{map[string]any{"type": "output_text", "text": "result"}}},
		},
	}
	body, _ := json.Marshal(resp)
	gotBody, err := AnthropicRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(gotBody, &got)
	if len(got.Messages) != 1 {
		t.Fatalf("messages=%v", got.Messages)
	}
	blocks, _ := got.Messages[0]["content"].([]any)
	tr, _ := blocks[0].(map[string]any)
	if tr["content"] != "result" {
		t.Errorf("array output dropped; got %v", tr)
	}
}
