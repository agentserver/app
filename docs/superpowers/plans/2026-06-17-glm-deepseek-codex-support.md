# GLM / DeepSeek Codex Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Codex use `glm-5.2[1m]` (via Anthropic Messages) and `deepseek-v4-pro` (via Chat Completions) in addition to `gpt-5.5`, by adding a per-request protocol-conversion layer to the existing local proxy.

**Architecture:** Codex only speaks the OpenAI Responses API. The local proxy (`internal/modelproxy`, `127.0.0.1:53452`) peeks the `model` field from each `/v1/responses` request and dispatches: `gpt-5.5` → pass-through; `deepseek-v4-pro` → a new `internal/protoconv` converter (Responses⇄Chat Completions); `glm-5.2[1m]` → another converter (Responses⇄Anthropic Messages). The proxy owns transport/auth/token; `protoconv` owns pure translation. A `set-model` subcommand on both CLIs rewrites `~/.codex/config.toml`'s `model` field.

**Tech Stack:** Go 1.x, `encoding/json`, `net/http`/`httputil`, table-driven tests. Conversion logic references cc-switch / `responses-proxy` / llama-swap (read-only).

**Spec:** `docs/superpowers/specs/2026-06-17-glm-deepseek-codex-support-design.md`. The conversion mapping contract lives there (Conversion Contract §).

**Worktree:** `worktree-glm-deepseek-codex-support`, based on `origin/master` (contains `opencode-desktop-support`). All paths below are relative to the worktree root.

**Conventions:** match the repo — `errors`/`fmt`, table-driven tests, `go test -race -count=1`. Commit after each task. Reference the spec's Conversion Contract for any mapping detail not shown verbatim below.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/protoconv/catalog.go` | model → wire-protocol routing table; `LookupRoute`, `KnownModels` |
| `internal/protoconv/responses.go` | shared types: `Wire`, `Route`, Responses request/response/SSE structs |
| `internal/protoconv/chat.go` | Converter A: Responses⇄Chat Completions (deepseek) |
| `internal/protoconv/anthropic.go` | Converter B: Responses⇄Anthropic Messages (glm) |
| `internal/protoconv/*_test.go` | table-driven tests for each converter |
| `internal/modelproxy/proxy.go` | routing dispatch in `NewHandler` |
| `internal/modelproxy/proxy_test.go` | routing cases (gpt-5.5 / deepseek / glm / unknown) |
| `cmd/agentctl/cmd_set_model.go` + `cmd/agentctl/main.go` | `agentctl set-model <name>` |
| `cmd/agentctl/cmd_set_model_test.go` | validation + config-rewrite test |
| `cmd/agentserver/main.go` (+ wiring) | `agentserver set-model <name>` |

`internal/codex/config.go` needs **no code change** — `ModelserverSettings()` keeps the `gpt-5.5` default and callers already override `s.Model` (verified unchanged on `origin/master`).

---

## Task 1: Catalog + shared types

**Files:**
- Create: `internal/protoconv/responses.go`
- Create: `internal/protoconv/catalog.go`
- Create: `internal/protoconv/catalog_test.go`

- [ ] **Step 1: Write the failing test**

`internal/protoconv/catalog_test.go`:
```go
package protoconv

import "testing"

func TestLookupRoute(t *testing.T) {
	cases := []struct {
		model string
		want  Wire
		ok    bool
	}{
		{"gpt-5.5", WireResponses, true},
		{"deepseek-v4-pro", WireChat, true},
		{"glm-5.2[1m]", WireAnthropic, true},
		{"does-not-exist", "", false},
	}
	for _, c := range cases {
		r, ok := LookupRoute(c.model)
		if ok != c.ok {
			t.Errorf("LookupRoute(%q) ok = %v, want %v", c.model, ok, c.ok)
			continue
		}
		if ok && r.Wire != c.want {
			t.Errorf("LookupRoute(%q) wire = %q, want %q", c.model, r.Wire, c.want)
		}
	}
}

func TestKnownModels(t *testing.T) {
	got := KnownModels()
	want := map[string]bool{"gpt-5.5": true, "deepseek-v4-pro": true, "glm-5.2[1m]": true}
	if len(got) != len(want) {
		t.Fatalf("KnownModels() = %v, want %d entries", got, len(want))
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("KnownModels() unexpected entry %q", m)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protoconv/`
Expected: FAIL (package doesn't exist / types undefined).

- [ ] **Step 3: Write the implementation**

`internal/protoconv/responses.go`:
```go
// Package protoconv translates Codex's OpenAI Responses API requests to and
// from the wire protocols the model gateway exposes for non-OpenAI models
// (Chat Completions for deepseek-v4-pro, Anthropic Messages for glm-5.2[1m]).
package protoconv

// Wire names the upstream protocol a model is served through.
type Wire string

const (
	// WireResponses passes the request through to /v1/responses unchanged.
	WireResponses Wire = "responses"
	// WireChat converts to OpenAI Chat Completions (/v1/chat/completions).
	WireChat Wire = "chat"
	// WireAnthropic converts to Anthropic Messages (/v1/messages).
	WireAnthropic Wire = "anthropic"
)

// Route binds a model name to its upstream wire protocol.
type Route struct {
	Model string
	Wire  Wire
}
```

`internal/protoconv/catalog.go`:
```go
package protoconv

// catalog is the single source of truth for which models the proxy knows how
// to route. Add a model = add a row. Buckets mirror opencode's
// responsesModels / compatibleModels / anthropicModels.
var catalog = []Route{
	{Model: "gpt-5.5", Wire: WireResponses},
	{Model: "deepseek-v4-pro", Wire: WireChat},
	{Model: "glm-5.2[1m]", Wire: WireAnthropic},
}

// LookupRoute returns the route for a model name and whether it is known.
func LookupRoute(model string) (Route, bool) {
	for _, r := range catalog {
		if r.Model == model {
			return r, true
		}
	}
	return Route{}, false
}

// KnownModels returns the catalog model names, for set-model validation.
func KnownModels() []string {
	out := make([]string, 0, len(catalog))
	for _, r := range catalog {
		out = append(out, r.Model)
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protoconv/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/protoconv/responses.go internal/protoconv/catalog.go internal/protoconv/catalog_test.go
git commit -m "feat(protoconv): add model routing catalog"
```

---

## Task 2: Chat converter — request mapper (Responses → Chat Completions)

**Files:**
- Create: `internal/protoconv/chat.go`
- Create: `internal/protoconv/chat_test.go`

- [ ] **Step 1: Write the failing test**

`internal/protoconv/chat_test.go` (request mapping — the contract):
```go
package protoconv

import (
	"encoding/json"
	"testing"
)

func TestChatRequestFromResponses(t *testing.T) {
	// Minimal: string input + instructions + a function tool.
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
	// Array input with message, function_call, function_call_output.
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

	// user message content flattened to a string
	if got.Messages[0]["role"] != "user" {
		t.Errorf("msg0 role = %v, want user", got.Messages[0]["role"])
	}
	if _, ok := got.Messages[0]["content"].(string); !ok {
		t.Errorf("msg0 content should be flattened string, got %T", got.Messages[0]["content"])
	}
	// assistant message with tool_calls
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
	// tool message
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protoconv/ -run TestChatRequest`
Expected: FAIL (`ChatRequestFromResponses` undefined).

- [ ] **Step 3: Write the implementation**

`internal/protoconv/chat.go`:
```go
package protoconv

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ChatUpstreamPath is where Chat Completions requests go on the gateway.
const ChatUpstreamPath = "/v1/chat/completions"

// ChatRequestFromResponses translates a Codex Responses request body to an
// OpenAI Chat Completions request body. See spec "Converter A".
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
		out["reasoning"] = r // forwarded where upstream accepts it
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
				// v1: dropped. file/audio/reasoning parity is a follow-up (spec #7).
			}
		}
	}

	if tools, ok := root["tools"].([]any); ok {
		conv := make([]any, 0, len(tools))
		for _, t := range tools {
			tm, ok := t.(map[string]any)
			if !ok || tm["type"] != "function" {
				continue // non-function tool types logged-and-dropped (spec)
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protoconv/ -run TestChatRequest`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/protoconv/chat.go internal/protoconv/chat_test.go
git commit -m "feat(protoconv): Responses->Chat Completions request mapper"
```

---

## Task 3: Chat converter — non-stream response assembler (Chat → Responses)

**Files:**
- Modify: `internal/protoconv/chat.go`
- Modify: `internal/protoconv/chat_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/protoconv/chat_test.go`:
```go
func TestChatResponseToResponses(t *testing.T) {
	// A Chat Completions body with text + one tool call.
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
	// first item should be a message carrying the text
	first := output[0].(map[string]any)
	if first["type"] != "message" {
		t.Errorf("first output type = %v, want message", first["type"])
	}
	// a function_call item must be present
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protoconv/ -run TestChatResponse`
Expected: FAIL (`ChatResponseToResponses` undefined).

- [ ] **Step 3: Write the implementation**

Append to `internal/protoconv/chat.go`:
```go
// ChatResponseToResponses assembles a complete (non-streaming) Chat
// Completions response body into a Responses-shaped body. See spec "Converter A".
func ChatResponseToResponses(chatBody []byte) ([]byte, error) {
	var chat struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage any `json:"usage"`
	}
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		return nil, fmt.Errorf("protoconv: parse chat body: %w", err)
	}

	output := []any{}
	for _, ch := range chat.Choices {
		m := ch.Message
		if strings.TrimSpace(m.Content) != "" {
			output = append(output, map[string]any{
				"type": "message", "role": firstNonEmpty(m.Role, "assistant"),
				"content": []any{map[string]any{"type": "output_text", "text": m.Content}},
			})
		}
		for _, tc := range m.ToolCalls {
			output = append(output, map[string]any{
				"type": "function_call", "name": tc.Function.Name,
				"arguments": tc.Function.Arguments, "call_id": tc.ID,
			})
		}
	}

	resp := map[string]any{
		"id":     chat.ID,
		"model":  chat.Model,
		"status": "completed",
		"output": output,
		"usage":  chat.Usage,
	}
	return json.Marshal(resp)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protoconv/ -run TestChatResponse`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/protoconv/chat.go internal/protoconv/chat_test.go
git commit -m "feat(protoconv): Chat Completions->Responses response assembler"
```

---

## Task 4: Chat converter — streaming (SSE)

> **Contract-first.** The exact upstream chunk shape must be confirmed against the real gateway on the test box (`ssh Administrator@9.0.16.110`); the test below pins the *expected Responses output* for a representative canned Chat Completions stream. See spec "Converter A — Response (stream)" and reference cc-switch / llama-swap for the event sequence.

**Files:**
- Modify: `internal/protoconv/chat.go`
- Create: `internal/protoconv/chat_stream_test.go`

- [ ] **Step 1: Write the failing test (the contract)**

`internal/protoconv/chat_stream_test.go`:
```go
package protoconv

import (
	"bufio"
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
	// every line must be a valid SSE frame (event:/data:) or blank
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") {
			continue
		}
		t.Errorf("unexpected SSE line: %q", line)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protoconv/ -run TestChatStream`
Expected: FAIL (`WriteChatStreamAsResponses` undefined).

- [ ] **Step 3: Write the implementation**

Append to `internal/protoconv/chat.go`:
```go
import (
	"bufio"
	"encoding/json"
	// ...existing imports...
	"io"
	"net/http"
)

// WriteChatStreamAsResponses reads a Chat Completions SSE stream from r and
// writes the equivalent Responses SSE event sequence to w. Event sequence per
// spec: response.created -> output_item.added -> output_text.delta /
// function_call_arguments.delta -> output_item.done -> response.completed.
func WriteChatStreamAsResponses(r io.Reader, w http.ResponseWriter) error {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeEvent := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	writeEvent("response.created", map[string]any{"type": "response.created"})

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	itemAdded := false
	// tool-call argument buffers keyed by tool index -> {id, name, args}
	toolBuf := map[int]map[string]string{}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Role      string `json:"role"`
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // skip malformed chunk
		}
		for _, ch := range chunk.Choices {
			d := ch.Delta
			if d.Content != "" {
				if !itemAdded {
					writeEvent("response.output_item.added", map[string]any{"type": "response.output_item.added"})
					itemAdded = true
				}
				writeEvent("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": d.Content})
			}
			for _, tc := range d.ToolCalls {
				buf := toolBuf[tc.Index]
				if buf == nil {
					buf = map[string]string{"id": tc.ID, "name": tc.Function.Name}
					toolBuf[tc.Index] = buf
					writeEvent("response.output_item.added", map[string]any{"type": "response.output_item.added"})
				}
				if tc.Function.Arguments != "" {
					writeEvent("response.function_call_arguments.delta", map[string]any{
						"type": "response.function_call_arguments.delta",
						"item_id": buf["id"], "delta": tc.Function.Arguments,
					})
					buf["args"] += tc.Function.Arguments
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}

	writeEvent("response.output_item.done", map[string]any{"type": "response.output_item.done"})
	writeEvent("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}})
	return nil
}
```

> NOTE for the implementer: validate the exact `data:` JSON shape of a real `deepseek-v4-pro` streaming response on the test box; adjust the `chunk` struct if the gateway uses `reasoning_content` or different `tool_calls` framing. The test above pins the Responses-side contract — keep it passing.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protoconv/ -run TestChatStream`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/protoconv/chat.go internal/protoconv/chat_stream_test.go
git commit -m "feat(protoconv): Chat Completions SSE -> Responses SSE"
```

---

## Task 5: Anthropic converter — request mapper (Responses → Anthropic Messages)

**Files:**
- Create: `internal/protoconv/anthropic.go`
- Create: `internal/protoconv/anthropic_test.go`

- [ ] **Step 1: Write the failing test**

`internal/protoconv/anthropic_test.go`:
```go
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
	// user text -> content block array
	u := msgs[0].(map[string]any)
	blocks, _ := u["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["type"] != "text" {
		t.Errorf("user content blocks = %v", blocks)
	}
	// assistant tool_use
	asst := msgs[1].(map[string]any)
	if asst["role"] != "assistant" {
		t.Errorf("msg1 role = %v", asst["role"])
	}
	// tool_result
	tr := msgs[2].(map[string]any)
	if tr["role"] != "user" {
		t.Errorf("msg2 role = %v, want user (tool_result)", tr["role"])
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "run" {
		t.Errorf("tools = %v", tools)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protoconv/ -run TestAnthropicRequest`
Expected: FAIL (`AnthropicRequestFromResponses` undefined).

- [ ] **Step 3: Write the implementation**

`internal/protoconv/anthropic.go`:
```go
package protoconv

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AnthropicUpstreamPath is where Anthropic Messages requests go on the gateway.
const AnthropicUpstreamPath = "/v1/messages"

// AnthropicRequestFromResponses translates a Codex Responses request body to
// an Anthropic Messages request body. See spec "Converter B".
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
				// v1: dropped (spec #7).
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protoconv/ -run TestAnthropicRequest`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/protoconv/anthropic.go internal/protoconv/anthropic_test.go
git commit -m "feat(protoconv): Responses->Anthropic Messages request mapper"
```

---

## Task 6: Anthropic converter — non-stream response assembler

**Files:**
- Modify: `internal/protoconv/anthropic.go`
- Modify: `internal/protoconv/anthropic_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/protoconv/anthropic_test.go`:
```go
func TestAnthropicResponseToResponses(t *testing.T) {
	ant := map[string]any{
		"id":    "msg_1",
		"model": "glm-5.2[1m]",
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protoconv/ -run TestAnthropicResponse`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**

Append to `internal/protoconv/anthropic.go`:
```go
// AnthropicResponseToResponses assembles a complete (non-streaming) Anthropic
// Messages response body into a Responses-shaped body. See spec "Converter B".
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protoconv/ -run TestAnthropicResponse`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/protoconv/anthropic.go internal/protoconv/anthropic_test.go
git commit -m "feat(protoconv): Anthropic Messages->Responses response assembler"
```

---

## Task 7: Anthropic converter — streaming (SSE)

> **Contract-first, like Task 4.** Anthropic content-block streaming (`message_start`/`content_block_start`/`content_block_delta`/`content_block_stop`/`message_delta`/`message_stop`) is the harder of the two — validate against a real `glm-5.2[1m]` stream on the test box.

**Files:**
- Modify: `internal/protoconv/anthropic.go`
- Create: `internal/protoconv/anthropic_stream_test.go`

- [ ] **Step 1: Write the failing test (the contract)**

`internal/protoconv/anthropic_stream_test.go`:
```go
package protoconv

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicStreamToResponses(t *testing.T) {
	const sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"glm-5.2[1m]\"}}\n\n" +
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protoconv/ -run TestAnthropicStream`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**

Append to `internal/protoconv/anthropic.go`:
```go
import (
	"bufio"
	"io"
	"net/http"
)

// WriteAnthropicStreamAsResponses reads an Anthropic Messages SSE stream and
// writes the Responses SSE event sequence. See spec "Converter B — Response".
func WriteAnthropicStreamAsResponses(r io.Reader, w http.ResponseWriter) error {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeEvent := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	writeEvent("response.created", map[string]any{"type": "response.created"})

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var event string
	itemAdded := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			if (event == "content_block_start" || event == "message_start") && !itemAdded {
				writeEvent("response.output_item.added", map[string]any{"type": "response.output_item.added"})
				itemAdded = true
			}
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
		case "content_block_delta":
			delta, _ := msg["delta"].(map[string]any)
			if delta["type"] == "text_delta" {
				if text, _ := delta["text"].(string); text != "" {
					writeEvent("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": text})
				}
			}
			// input_json_delta (tool_use args) -> function_call_arguments.delta
			if delta["type"] == "input_json_delta" {
				if pj, _ := delta["partial_json"].(string); pj != "" {
					writeEvent("response.function_call_arguments.delta", map[string]any{
						"type": "response.function_call_arguments.delta", "delta": pj,
					})
				}
			}
		case "message_stop":
			writeEvent("response.output_item.done", map[string]any{"type": "response.output_item.done"})
			writeEvent("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}})
		}
	}
	return scanner.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protoconv/ -run TestAnthropicStream`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/protoconv/anthropic.go internal/protoconv/anthropic_stream_test.go
git commit -m "feat(protoconv): Anthropic Messages SSE -> Responses SSE"
```

---

## Task 8: Proxy routing integration

> Extend `NewHandler` in `internal/modelproxy/proxy.go`: after the existing token injection + instructions normalization, peek `model` and dispatch converted models through `protoconv`; everything else falls through to the existing reverse proxy.

**Files:**
- Modify: `internal/modelproxy/proxy.go`
- Modify: `internal/modelproxy/proxy_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/modelproxy/proxy_test.go`:
```go
func TestHandlerRoutesDeepseekToChatCompletions(t *testing.T) {
	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","model":"deepseek-v4-pro","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`)
	}))
	defer upstream.Close()

	h, err := modelproxy.NewHandler(modelproxy.Options{
		Secrets:          stubSecrets("token"),
		UpstreamBaseURL:  upstream.URL,
		LocalBearerToken: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	body := []byte(`{"model":"deepseek-v4-pro","input":"hi","stream":false}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if gotPath != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", gotPath)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(gotBody), &sent)
	if sent["model"] != "deepseek-v4-pro" {
		t.Errorf("upstream body model = %v", sent["model"])
	}
	// client gets a Responses-shaped body
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	if out["status"] != "completed" {
		t.Errorf("client status = %v, want completed", out["status"])
	}
}
```

If `stubSecrets`, `io`, `json`, `bytes`, `fmt` are not already imported in the test file, add them. `stubSecrets` may already exist in `proxy_test.go` — reuse it; if not, add a minimal stub implementing `secrets.Store`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/modelproxy/ -run TestHandlerRoutesDeepseek`
Expected: FAIL (handler currently passes everything straight to `/v1/responses`, so `gotPath` will be `/v1/responses`).

- [ ] **Step 3: Write the implementation**

In `internal/modelproxy/proxy.go`, add the import `"github.com/agentserver/agentserver-pkg/internal/protoconv"` and `"io"` (if not present). Replace the handler closure's tail so that, after `normalizeResponsesInstructions`, it decides whether to convert:

```go
	// peek model and dispatch converted models through protoconv; otherwise
	// fall through to the existing reverse proxy (responses pass-through).
	if converted, path, convBody, ok := convertIfCatalogued(r2); ok {
		serveConverted(r2.Context(), opts, upstream, converted, path, convBody, w, r2)
		return
	}
	r2.Header.Del("X-AgentServer-Client")
	proxy.ServeHTTP(w, r2)
```

Add helper functions in the same file:
```go
// convertIfCatalogued reads the request body; if the model is a converted one,
// it returns the converted upstream body + path. It always restores r2.Body.
func convertIfCatalogued(r *http.Request) (wire protoconv.Wire, path string, convBody []byte, ok bool) {
	if r.Method != http.MethodPost {
		return "", "", nil, false
	}
	trimmed := strings.TrimRight(r.URL.Path, "/")
	if trimmed != "/v1/responses" && trimmed != "/responses" {
		return "", "", nil, false
	}
	raw, err := io.ReadAll(r.Body)
	r.Body.Close()
	defer func() {
		// restore the original body for the fall-through path
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
	}()
	if err != nil {
		return "", "", nil, false
	}
	var peek struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return "", "", nil, false
	}
	route, found := protoconv.LookupRoute(peek.Model)
	if !found || route.Wire == protoconv.WireResponses {
		return "", "", nil, false
	}
	switch route.Wire {
	case protoconv.WireChat:
		body, err := protoconv.ChatRequestFromResponses(raw)
		if err != nil {
			return "", "", nil, false
		}
		return route.Wire, protoconv.ChatUpstreamPath, body, true
	case protoconv.WireAnthropic:
		body, err := protoconv.AnthropicRequestFromResponses(raw)
		if err != nil {
			return "", "", nil, false
		}
		return route.Wire, protoconv.AnthropicUpstreamPath, body, true
	}
	return "", "", nil, false
}

// serveConverted POSTs the converted body upstream and writes the translated
// Responses response (streaming-aware) back to the client.
func serveConverted(ctx context.Context, opts Options, upstream *url.URL, wire protoconv.Wire, path string, convBody []byte, w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			http.Error(w, "model proxy conversion error", http.StatusBadGateway)
		}
	}()

	token, err := opts.Secrets.Get(tokenrefresh.AccessTokenKey)
	if err != nil || token == "" {
		http.Error(w, "modelserver login required", http.StatusUnauthorized)
		return
	}
	stream := bytes.Contains(convBody, []byte(`"stream":true`))
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.Scheme+"://"+upstream.Host+path, bytes.NewReader(convBody))
	if err != nil {
		http.Error(w, "model proxy upstream request", http.StatusBadGateway)
		return
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+token)
	if wire == protoconv.WireAnthropic {
		upReq.Header.Set("X-Api-Key", token)
	}
	client := &http.Client{}
	if opts.Transport != nil {
		client.Transport = opts.Transport
	}
	resp, err := client.Do(upReq)
	if err != nil {
		http.Error(w, "model proxy upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	switch {
	case wire == protoconv.WireChat && stream:
		_ = protoconv.WriteChatStreamAsResponses(resp.Body, w)
	case wire == protoconv.WireChat:
		b, _ := io.ReadAll(resp.Body)
		out, err := protoconv.ChatResponseToResponses(b)
		if err != nil {
			http.Error(w, "model proxy conversion error", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	case wire == protoconv.WireAnthropic && stream:
		_ = protoconv.WriteAnthropicStreamAsResponses(resp.Body, w)
	case wire == protoconv.WireAnthropic:
		b, _ := io.ReadAll(resp.Body)
		out, err := protoconv.AnthropicResponseToResponses(b)
		if err != nil {
			http.Error(w, "model proxy conversion error", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}
}
```

> `context` is already imported in proxy.go; ensure `bytes`, `io`, `net/url`, `encoding/json` are imported too. The existing `normalizeResponsesInstructions` already reads/rewrites the body; since `convertIfCatalogued` runs *after* it, read `r2.Body` (the normalized body) — the restore in the deferred func keeps the reverse-proxy fall-through working.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/modelproxy/`
Expected: PASS (new test + existing tests still pass).

- [ ] **Step 5: Commit**

```bash
git add internal/modelproxy/proxy.go internal/modelproxy/proxy_test.go
git commit -m "feat(modelproxy): route converted models through protoconv"
```

---

## Task 9: Codex config non-default model test

> No code change to `internal/codex` — just a regression test proving a non-default `Model` lands in `config.toml`. (Confirms `ModelserverSettings()` default + override pattern.)

**Files:**
- Modify: `internal/codex/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/codex/config_test.go`:
```go
func TestUpdateConfigWritesNonDefaultModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	s := ModelserverSettings()
	s.Model = "glm-5.2[1m]"
	if err := UpdateConfig(path, s); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `model = "glm-5.2[1m]"`) {
		t.Errorf("config missing overridden model; got:\n%s", b)
	}
}
```
Ensure `filepath`, `os`, `strings` are imported (they already are in that file).

- [ ] **Step 2: Run test to verify it passes (no impl change expected)**

Run: `go test ./internal/codex/`
Expected: PASS immediately (the override already flows through the existing merge).

- [ ] **Step 3: Commit**

```bash
git add internal/codex/config_test.go
git commit -m "test(codex): non-default model overrides config.toml model field"
```

---

## Task 10: `agentctl set-model`

**Files:**
- Create: `cmd/agentctl/cmd_set_model.go`
- Create: `cmd/agentctl/cmd_set_model_test.go`
- Modify: `cmd/agentctl/main.go`

- [ ] **Step 1: Write the failing test**

`cmd/agentctl/cmd_set_model_test.go`:
```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
)

func TestWriteModelSelection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// seed a valid codex config so we only rewrite the model field
	if err := codex.UpdateConfig(path, codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, "t")); err != nil {
		t.Fatal(err)
	}
	if err := writeModelSelection(path, "deepseek-v4-pro"); err != nil {
		t.Fatalf("writeModelSelection: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !contains(string(b), `model = "deepseek-v4-pro"`) {
		t.Errorf("model field not updated; got:\n%s", b)
	}
	// other keys preserved
	if !contains(string(b), `model_provider = "modelserver"`) {
		t.Errorf("model_provider clobbered; got:\n%s", b)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```
> If `strings` import is acceptable, replace the toy `contains`/`indexOf` with `strings.Contains`. Keep the test self-contained otherwise.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/agentctl/ -run TestWriteModelSelection`
Expected: FAIL (`writeModelSelection` undefined).

- [ ] **Step 3: Write the implementation**

`cmd/agentctl/cmd_set_model.go`:
```go
package main

import (
	"fmt"
	"os"

	"github.com/agentserver/agentserver-pkg/internal/codex"
	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/protoconv"
)

func runSetModel(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: agentctl set-model <name>")
		fmt.Fprintf(os.Stderr, "known models: %v\n", protoconv.KnownModels())
		os.Exit(2)
	}
	model := args[0]
	if _, ok := protoconv.LookupRoute(model); !ok {
		fmt.Fprintf(os.Stderr, "unknown model %q; known models: %v\n", model, protoconv.KnownModels())
		os.Exit(2)
	}
	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	if err := writeModelSelection(p.CodexConfigFile, model); err != nil {
		die(err)
	}
	fmt.Printf("model set to %s in %s\n", model, p.CodexConfigFile)
}

// writeModelSelection rewrites only the model field of the Codex config,
// preserving the rest (provider, wire_api, etc.).
func writeModelSelection(path, model string) error {
	settings := codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, codex.LegacyLocalProxyAPIKeyValue)
	settings.Model = model
	return codex.UpdateConfig(path, settings)
}
```

Wire it into `cmd/agentctl/main.go` — add to the switch:
```go
	case "set-model":
		runSetModel(os.Args[2:])
```
and add to the `usage()` text:
```
  agentctl set-model <name>      set the Codex model (gpt-5.5 / deepseek-v4-pro / glm-5.2[1m])
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/agentctl/ -run TestWriteModelSelection`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agentctl/cmd_set_model.go cmd/agentctl/cmd_set_model_test.go cmd/agentctl/main.go
git commit -m "feat(agentctl): add set-model subcommand"
```

---

## Task 11: `agentserver set-model` (Linux headless)

**Files:**
- Modify: `cmd/agentserver/main.go` (and the `app` struct + `run` dispatch + wiring in `newApp`)

- [ ] **Step 1: Read the current dispatch**

Run: `sed -n '1,200p' cmd/agentserver/main.go` to see the `app.run` switch and `newApp` wiring. The pattern: each action is a function field on `app`, dispatched in `run`, wired in `newApp`.

- [ ] **Step 2: Add the `set-model` action**

Add a field `setModel func(args []string) error` to the `app` struct. In `run`'s switch, add:
```go
	case "set-model":
		return a.setModel(args[1:])
```
In `newApp`, wire it (reuse the same helper as agentctl — move `writeModelSelection` to a shared spot, or re-implement inline):
```go
	setModel: func(args []string) error {
		return runAgentserverSetModel(args)
	},
```
Add a small function in the same package:
```go
func runAgentserverSetModel(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentserver set-model <name>; known: %v", protoconv.KnownModels())
	}
	model := args[0]
	if _, ok := protoconv.LookupRoute(model); !ok {
		return fmt.Errorf("unknown model %q; known: %v", model, protoconv.KnownModels())
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	settings := codex.ModelserverProxySettings(modelproxy.DefaultBaseURL, codex.LegacyLocalProxyAPIKeyValue)
	settings.Model = model
	if err := codex.UpdateConfig(p.CodexConfigFile, settings); err != nil {
		return err
	}
	fmt.Printf("model set to %s in %s\n", model, p.CodexConfigFile)
	return nil
}
```
> To avoid duplicating logic between agentctl and agentserver, prefer extracting `writeModelSelection(path, model)` into `internal/codex` (e.g. `codex.SetModel(path, model) error`) and call it from both. If you do, update Task 10 to call it too. Keep it DRY.

- [ ] **Step 3: Build + test**

Run: `go build ./cmd/agentserver && go test ./cmd/agentserver/`
Expected: build succeeds; existing tests pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/agentserver/main.go
git commit -m "feat(agentserver): add set-model subcommand"
```

---

## Task 12: End-to-end manual verification (Windows test box)

> Build the proxy, deploy to the test box, and validate real Codex + `code.ai.cs.ac.cn` for all three models. This is the acceptance gate for the contract-first streaming code (Tasks 4 & 7).

- [ ] **Step 1: Build for Windows**

Run: `make cross-windows` (produces the Windows binaries in `dist/`).

- [ ] **Step 2: Deploy to the test box**

Run: `scp` the built launcher/daemon to `Administrator@9.0.16.110` and start the local proxy + token refresher the way the Windows installer would (or run the onboarding once). Confirm the proxy listens on `127.0.0.1:53452` and `~/.codex/config.toml` points `base_url` at it with `wire_api = "responses"`.

- [ ] **Step 3: Set + exercise each model**

```bash
# on the box
agentctl set-model gpt-5.5          # baseline: must work as before
agentctl set-model deepseek-v4-pro # chat converter
agentctl set-model glm-5.2[1m]     # anthropic converter
```
For each, run a real Codex session: a plain chat, and a coding task that triggers a tool call (e.g. an edit). Confirm text streams correctly and tool calls round-trip.

- [ ] **Step 4: Capture and fix streaming edge cases**

If `deepseek-v4-pro` or `glm-5.2[1m]` streams look wrong, capture a raw upstream chunk (`curl` to `code.ai.cs.ac.cn/v1/chat/completions` and `/v1/messages` with the real key) and adjust the `chunk`/`msg` structs in Task 4 / Task 7 to match. Re-run those unit tests against the real shape; keep the contract test green.

- [ ] **Step 5: Commit any fixes**

```bash
git add internal/protoconv/*.go
git commit -m "fix(protoconv): align streaming structs with real gateway shapes"
```

- [ ] **Step 6: Full verification**

Run: `go test -race -count=1 ./...`
Expected: all packages pass.

---

## Notes for implementers

- **DRY:** the model-selection write logic is the same in agentctl and agentserver — extract `codex.SetModel(path, model)` and reuse (Task 11 note).
- **`internal/codex/config.go` needs no structural change** — the override-via-`s.Model` pattern is already supported (Task 9 proves it).
- **Streaming is contract-first:** the unit tests pin the Responses-side output; confirm the upstream-side shapes against the real gateway on the test box (Task 12 Step 4) before declaring done.
- **`gpt-5.5` behavior must not change** — it still pass-through-proxies to `/v1/responses`. The existing `proxy_test.go` cases guard this.
