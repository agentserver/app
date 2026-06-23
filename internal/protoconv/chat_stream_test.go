package protoconv

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatStreamToResponses(t *testing.T) {
	// Canned Chat Completions SSE: one text delta, one tool-call arg delta, finish.
	const sse = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"run\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"

	rec := httptest.NewRecorder()
	r := strings.NewReader(sse)
	err := WriteChatStreamAsResponses(r, rec)
	if err != nil {
		t.Fatalf("WriteChatStreamAsResponses: %v", err)
	}

	body := rec.Body.String()
	mustContain := []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.output_text.delta",
		`"hi"`,
		"event: response.function_call_arguments.delta",
		"event: response.completed",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("stream output missing %q\n--- got ---\n%s", want, body)
		}
	}
	// every non-empty line must be a valid SSE frame (event:/data:)
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") {
			continue
		}
		t.Errorf("unexpected SSE line: %q", line)
	}
}

// Regression: mirror of the Anthropic shape test. Codex parser silently
// drops items that don't match ResponseItem.
func TestChatStreamItemShapesAreCodexParseable(t *testing.T) {
	const sse = "data: {\"id\":\"x\",\"model\":\"deepseek-v4-pro\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Hi \"}}]}\n\n" +
		"data: {\"id\":\"x\",\"model\":\"deepseek-v4-pro\",\"choices\":[{\"delta\":{\"content\":\"there\"}}]}\n\n" +
		"data: {\"id\":\"x\",\"model\":\"deepseek-v4-pro\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"run\",\"arguments\":\"{\\\"k\\\":\"}}]}}]}\n\n" +
		"data: {\"id\":\"x\",\"model\":\"deepseek-v4-pro\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
		"data: {\"id\":\"x\",\"model\":\"deepseek-v4-pro\",\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	rec := httptest.NewRecorder()
	if err := WriteChatStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	var doneItems []map[string]any
	for _, frame := range strings.Split(body, "\n\n") {
		if !strings.Contains(frame, "event: response.output_item.done") {
			continue
		}
		dataIdx := strings.Index(frame, "data:")
		var ev struct {
			Item map[string]any `json:"item"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(frame[dataIdx+len("data:"):])), &ev); err != nil {
			t.Fatalf("malformed done frame: %v", err)
		}
		doneItems = append(doneItems, ev.Item)
	}
	if len(doneItems) != 2 {
		t.Fatalf("want 2 done items (msg+tool), got %d:\n%s", len(doneItems), body)
	}
	msg := doneItems[0]
	if msg["role"] != "assistant" {
		t.Errorf("msg missing role:assistant: %v", msg)
	}
	content := msg["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "Hi there" {
		t.Errorf("msg content not accumulated: %v", content)
	}
	fc := doneItems[1]
	if fc["name"] != "run" || fc["arguments"] != `{"k":1}` || fc["call_id"] != "c1" {
		t.Errorf("function_call missing fields: %v", fc)
	}
}

// Regression: matches the Anthropic-side test. Codex parser requires id on
// response.completed; the Chat SSE format carries it on each chunk's top-level
// `id`. Pick up the first non-empty one we see.
func TestChatStreamCarriesResponseIDAndModel(t *testing.T) {
	const sse = "data: {\"id\":\"chatcmpl-xyz\",\"model\":\"deepseek-v4-pro\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-xyz\",\"model\":\"deepseek-v4-pro\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	rec := httptest.NewRecorder()
	if err := WriteChatStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	for _, ev := range []string{"response.created", "response.completed"} {
		idx := strings.Index(body, "event: "+ev)
		if idx < 0 {
			t.Errorf("event %s missing\n%s", ev, body)
			continue
		}
		end := strings.Index(body[idx:], "\n\n")
		frame := body[idx : idx+end]
		if !strings.Contains(frame, `"id":"chatcmpl-xyz"`) {
			t.Errorf("event %s missing id:\n%s", ev, frame)
		}
		if !strings.Contains(frame, `"model":"deepseek-v4-pro"`) {
			t.Errorf("event %s missing model:\n%s", ev, frame)
		}
	}
}

func TestChatStreamMultiItemBalanced(t *testing.T) {
	const sse = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"a\",\"arguments\":\"{\\\"x\\\":1}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"c2\",\"function\":{\"name\":\"b\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	rec := httptest.NewRecorder()
	if err := WriteChatStreamAsResponses(strings.NewReader(sse), rec); err != nil {
		t.Fatal(err)
	}
	added, done, argWithItem := countItemEvents(rec.Body.String())
	if added != done {
		t.Errorf("added=%d done=%d (must balance);\n%s", added, done, rec.Body.String())
	}
	if added != 3 { // 1 message + 2 function calls
		t.Errorf("added=%d, want 3", added)
	}
	if argWithItem < 2 {
		t.Errorf("arg deltas with item_id = %d, want >=2", argWithItem)
	}
}

func TestChatRequestFunctionCallOutputArray(t *testing.T) {
	resp := map[string]any{
		"model": "deepseek-v4-pro",
		"input": []any{
			map[string]any{"type": "function_call_output", "call_id": "c1",
				"output": []any{map[string]any{"type": "output_text", "text": "result"}}},
		},
	}
	body, _ := json.Marshal(resp)
	gotBody, err := ChatRequestFromResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	_ = json.Unmarshal(gotBody, &got)
	if len(got.Messages) != 1 || got.Messages[0]["content"] != "result" {
		t.Errorf("array function_call_output dropped; messages=%v", got.Messages)
	}
}
