package protoconv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
				output := flattenResponsesContent(m["output"])
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

// ChatResponseToResponses assembles a complete (non-streaming) Chat
// Completions response body into a Responses-shaped body.
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

// WriteChatStreamAsResponses reads a Chat Completions SSE stream from r and
// writes the equivalent Responses SSE event sequence to w. Each output item
// (message, and each tool call) gets a balanced added/done pair; deltas carry item_id.
func WriteChatStreamAsResponses(r io.Reader, w http.ResponseWriter) error {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	writeEvent := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	counter := 0
	newID := func(prefix string) string { counter++; return fmt.Sprintf("%s_%d", prefix, counter) }

	var msgItemID string          // open message item; "" = none
	toolItems := map[int]string{} // open tool-call index -> item id

	// Upstream response identity, captured from the first chunk. Codex's
	// Responses parser fails ("missing field `id`") without these.
	respID := ""
	respModel := ""
	createdEmitted := false
	emitCreated := func() {
		if createdEmitted {
			return
		}
		createdEmitted = true
		writeEvent("response.created", map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":     respID,
				"model":  respModel,
				"status": "in_progress",
			},
		})
	}

	closeMsg := func() {
		if msgItemID != "" {
			writeEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "id": msgItemID}})
			msgItemID = ""
		}
	}
	closeTool := func(idx int) {
		if id, ok := toolItems[idx]; ok {
			writeEvent("response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "id": id, "call_id": id}})
			delete(toolItems, idx)
		}
	}
	closeAll := func() {
		closeMsg()
		for idx := range toolItems {
			closeTool(idx)
		}
	}

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
			ID      string `json:"id"`
			Model   string `json:"model"`
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
		if respID == "" && chunk.ID != "" {
			respID = chunk.ID
		}
		if respModel == "" && chunk.Model != "" {
			respModel = chunk.Model
		}
		emitCreated()
		for _, ch := range chunk.Choices {
			d := ch.Delta
			if d.Content != "" {
				if msgItemID == "" {
					msgItemID = newID("msg")
					writeEvent("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "message", "id": msgItemID}})
				}
				writeEvent("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": msgItemID, "delta": d.Content})
			}
			for _, tc := range d.ToolCalls {
				if _, ok := toolItems[tc.Index]; !ok {
					id := tc.ID
					if id == "" {
						id = newID("fc")
					}
					toolItems[tc.Index] = id
					writeEvent("response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "function_call", "id": id, "call_id": id, "name": tc.Function.Name}})
				}
				if tc.Function.Arguments != "" {
					writeEvent("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": toolItems[tc.Index], "delta": tc.Function.Arguments})
				}
			}
			if ch.FinishReason != "" {
				closeAll()
			}
		}
	}
	closeAll()
	if err := sc.Err(); err != nil {
		return err
	}
	emitCreated() // edge case: empty stream
	writeEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     respID,
			"model":  respModel,
			"status": "completed",
		},
	})
	return nil
}
