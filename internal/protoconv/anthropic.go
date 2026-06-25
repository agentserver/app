package protoconv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AnthropicUpstreamPath is where Anthropic Messages requests go on the gateway.
const AnthropicUpstreamPath = "/v1/messages"

// AnthropicRequestFromResponses translates a Codex Responses request body to
// an Anthropic Messages request body.
func AnthropicRequestFromResponses(respBody []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(respBody, &root); err != nil {
		return nil, fmt.Errorf("protoconv: parse responses body: %w", err)
	}

	out := map[string]any{
		"model": root["model"],
	}
	// Only forward stream when it's actually a bool in the source request;
	// see chat.go for the same omission rationale.
	if s, ok := root["stream"].(bool); ok {
		out["stream"] = s
	}
	// Anthropic Messages exposes a top-level `tool_choice` object with a
	// different shape than Codex's Responses string ({"type":"auto"|"any"|"tool"}).
	// Without a safe shape mapping we deliberately drop it; tool_choice is
	// rarely set by Codex 0.142 (default behavior leaves it unset, which is
	// equivalent to {"type":"auto"} on the Anthropic side).
	//
	// parallel_tool_calls is not part of the Anthropic Messages API — Anthropic
	// controls parallel tool calls server-side per model — so we also do not
	// forward it here. Both omissions are intentional.

	// Anthropic Messages API requires max_tokens. Map it from the Responses
	// request's max_output_tokens when present, else default.
	maxTokens := 8192
	if mot, ok := root["max_output_tokens"].(float64); ok && mot > 0 {
		maxTokens = int(mot)
	}
	out["max_tokens"] = maxTokens

	// Map the Responses API's top-level `reasoning.effort` to the Anthropic
	// Messages `thinking` field. Codex's wire shape is `reasoning:{effort,...}`;
	// Anthropic uses `thinking:{type:"enabled",budget_tokens:N}` (standard
	// extended-thinking contract). `none`/`minimal` disable thinking. The
	// budget is clamped to the Anthropic limits: > 1024 and strictly below
	// max_tokens (Anthropic rejects budget_tokens >= max_tokens).
	if thinking, ok := anthropicThinking(root["reasoning"], maxTokens); ok {
		out["thinking"] = thinking
	}
	// Prior reasoning items are replayed as Anthropic thinking blocks in the
	// input loop below (case "reasoning"): Codex echoes them with
	// encrypted_content (include=["reasoning.encrypted_content"]) and we map
	// that back to the Anthropic `signature` field. Anthropic requires thinking
	// blocks from the last assistant turn to be echoed back during tool-use
	// loops; omitting them causes a 400 invalid_request_error.

	// system: instructions + any developer/system input messages
	systemParts := []string{}
	if instr, _ := root["instructions"].(string); strings.TrimSpace(instr) != "" {
		systemParts = append(systemParts, instr)
	}

	messages := []any{}
	switch in := root["input"].(type) {
	case string:
		// Responses API permits a bare string as a single user message.
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": in}},
		})
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
					blocks := anthropicContentBlocks(m["content"])
					// If the last message is an assistant turn that currently holds only
					// echoed thinking blocks (no text/tool_use yet), merge this text into it
					// so thinking precedes text within one assistant turn, as Anthropic
					// requires. Otherwise a reasoning item followed by an assistant text
					// message would produce two separate assistant turns (thinking-only,
					// then text), which Anthropic rejects when the next turn carries tool_use.
					if role == "assistant" && len(messages) > 0 {
						if last, ok := messages[len(messages)-1].(map[string]any); ok && last["role"] == "assistant" {
							content, _ := last["content"].([]any)
							if len(content) > 0 && !hasTextOrToolUse(content) {
								last["content"] = append(content, blocks...)
								break
							}
						}
					}
					messages = append(messages, map[string]any{
						"role":    role,
						"content": blocks,
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
				block := map[string]any{
					"type": "tool_use", "id": callID, "name": name, "input": input,
				}
				// If the last message is an assistant message that only holds
				// echoed thinking blocks (no tool_use yet), append the tool_use
				// to it so thinking + tool_use share one assistant turn, as
				// Anthropic requires.
				if len(messages) > 0 {
					if last, ok := messages[len(messages)-1].(map[string]any); ok && last["role"] == "assistant" {
						content, _ := last["content"].([]any)
						if !hasToolUse(content) {
							last["content"] = append(content, block)
							break
						}
					}
				}
				messages = append(messages, map[string]any{
					"role":    "assistant",
					"content": []any{block},
				})
			case "function_call_output":
				callID, _ := m["call_id"].(string)
				output := flattenResponsesContent(m["output"])
				messages = append(messages, map[string]any{
					"role": "user",
					"content": []any{map[string]any{
						"type": "tool_result", "tool_use_id": callID, "content": output,
					}},
				})
			case "reasoning":
				// Replay the prior turn's thinking block: map Codex
				// encrypted_content back to the Anthropic `signature` field
				// (the opaque token Anthropic requires to be echoed verbatim).
				enc, _ := m["encrypted_content"].(string)
				if enc == "" {
					continue
				}
				block := map[string]any{"type": "thinking", "thinking": reasoningItemText(m), "signature": enc}
				// Thinking blocks must sit on an assistant message, before any
				// tool_use. Attach to the last assistant message if it has no
				// tool_use yet; otherwise start a new assistant message.
				attachThinkingBlock(&messages, block)
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

// anthropicThinking maps the Responses API `reasoning` object to Anthropic's
// `thinking` field. Returns the field value and ok=true when extended thinking
// should be enabled; ok=false (omit the field) when reasoning is absent, null,
// or set to a disabling effort.
//
// Codex's wire shape is `reasoning:{effort:"low"|"medium"|"high"|"xhigh"|
// "max"|...}` (the `ultra` config level is rewritten to "max" by Codex before
// sending). `effort` drives Anthropic's thinking budget: higher effort = larger
// budget_tokens. `none`/`minimal` disable thinking.
//
// Anthropic requires budget_tokens > 1024 and strictly < max_tokens. The
// budget is derived as a fraction of max_tokens per effort level and clamped
// to those bounds.
func anthropicThinking(reasoning any, maxTokens int) (map[string]any, bool) {
	rmap, ok := reasoning.(map[string]any)
	if !ok {
		// reasoning absent or null: do not enable thinking.
		return nil, false
	}
	effort, _ := rmap["effort"].(string)
	switch effort {
	case "", "none", "minimal":
		return nil, false
	}
	// Fraction of max_tokens reserved for thinking, increasing with effort.
	// `max` keeps the largest budget (bounded below by the 1024 floor).
	fraction := map[string]float64{
		"low":    0.25,
		"medium": 0.45,
		"high":   0.65,
		"xhigh":  0.80,
		"max":    0.90,
	}[effort]
	if fraction == 0 {
		// Unknown custom effort: default to a high budget so a non-standard
		// effort still engages thinking rather than silently disabling it.
		fraction = 0.65
	}
	budget := int(float64(maxTokens) * fraction)
	const minBudget = 1024
	// Anthropic rejects budget_tokens >= max_tokens; keep strictly below.
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget <= minBudget {
		// max_tokens is too small to admit a valid thinking budget; skip
		// thinking rather than send a request the gateway must reject.
		return nil, false
	}
	return map[string]any{
		"type":          "enabled",
		"budget_tokens": budget,
	}, true
}

// reasoningItemText extracts the plaintext thinking text from a Codex
// `reasoning` input item, if any. Codex carries it under content[] as
// {type:"reasoning_text", text}.
func reasoningItemText(m map[string]any) string {
	content, _ := m["content"].([]any)
	for _, c := range content {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["type"] == "reasoning_text" {
			if t, _ := cm["text"].(string); t != "" {
				return t
			}
		}
	}
	return ""
}

// attachThinkingBlock places an Anthropic thinking block onto an assistant
// message. Anthropic requires thinking blocks to be the first content of an
// assistant turn and consecutive; a thinking block may not follow a tool_use
// in the same message. If the last message is an assistant message with no
// tool_use blocks yet, append there; otherwise open a new assistant message.
func attachThinkingBlock(messages *[]any, block map[string]any) {
	if len(*messages) > 0 {
		last, ok := (*messages)[len(*messages)-1].(map[string]any)
		if ok && last["role"] == "assistant" {
			content, _ := last["content"].([]any)
			if !hasToolUse(content) {
				last["content"] = append(content, block)
				return
			}
		}
	}
	*messages = append(*messages, map[string]any{
		"role":    "assistant",
		"content": []any{block},
	})
}

// hasTextOrToolUse reports whether a content slice already contains a text or
// tool_use block. Used to decide whether an assistant turn that started with
// echoed thinking blocks can still absorb an incoming assistant text message:
// it can only while it holds thinking blocks alone.
func hasTextOrToolUse(content []any) bool {
	for _, c := range content {
		if cm, ok := c.(map[string]any); ok {
			switch cm["type"] {
			case "text", "tool_use":
				return true
			}
		}
	}
	return false
}

// hasToolUse reports whether a content slice already contains a tool_use block.
func hasToolUse(content []any) bool {
	for _, c := range content {
		if cm, ok := c.(map[string]any); ok && cm["type"] == "tool_use" {
			return true
		}
	}
	return false
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

// AnthropicResponseToResponses assembles a complete (non-streaming) Anthropic
// Messages response body into a Responses-shaped body.
func AnthropicResponseToResponses(antBody []byte) ([]byte, error) {
	var ant struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			Signature string          `json:"signature"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
		} `json:"content"`
		Usage any `json:"usage"`
	}
	if err := json.Unmarshal(antBody, &ant); err != nil {
		return nil, fmt.Errorf("protoconv: parse anthropic body: %w", err)
	}

	output := []any{}
	for _, b := range ant.Content {
		switch b.Type {
		case "thinking":
			// Emit a reasoning item carrying the Anthropic thinking signature as
			// encrypted_content. Codex echoes reasoning items back on the next
			// turn (when include=["reasoning.encrypted_content"]), and the
			// request converter maps encrypted_content back to the Anthropic
			// `signature` field. Without this round-trip, Anthropic rejects
			// tool-use loops that interleave thinking with a 400
			// invalid_request_error ("thinking blocks must be consecutive...").
			if b.Signature == "" {
				continue
			}
			reasoning := map[string]any{
				"type":              "reasoning",
				"encrypted_content": b.Signature,
			}
			if b.Thinking != "" {
				reasoning["content"] = []any{map[string]any{
					"type": "reasoning_text", "text": b.Thinking,
				}}
			}
			output = append(output, reasoning)
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

// WriteAnthropicStreamAsResponses reads an Anthropic Messages SSE stream and
// writes the Responses SSE event sequence. Each content block (text or tool_use)
// gets a balanced added/done pair; deltas carry item_id.
func WriteAnthropicStreamAsResponses(r io.Reader, w http.ResponseWriter) error {
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
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	counter := 0
	newID := func(prefix string) string { counter++; return fmt.Sprintf("%s_%d", prefix, counter) }

	var event string
	var curItemID string // id of the currently open content block; "" = none
	curToolID := ""      // if the open block is tool_use, the tool id; else ""
	curToolName := ""    // function name carried from content_block_start
	var textBuf strings.Builder
	var argsBuf strings.Builder
	// thinkingBuf/thinkingSig accumulate the open thinking block's text and
	// signature. Anthropic streams thinking as content_block_start{type:"thinking"}
	// + thinking_delta/signature_delta + content_block_stop; the signature must
	// be echoed back on the next turn, so we capture it into a reasoning item's
	// encrypted_content at block stop.
	var thinkingBuf strings.Builder
	var thinkingSig string

	// Upstream response identity, captured from message_start. Codex's
	// Responses parser rejects the stream with "missing field `id`" if these
	// are absent from response.created/response.completed.
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

	closeCurrent := func() {
		if curItemID == "" {
			return
		}
		if curToolID != "" {
			// Codex's ResponseItem::FunctionCall requires {name, arguments, call_id}.
			writeEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":      "function_call",
					"id":        curToolID,
					"call_id":   curToolID,
					"name":      curToolName,
					"arguments": argsBuf.String(),
				},
			})
			curToolID = ""
			curToolName = ""
			argsBuf.Reset()
		} else {
			// Codex's ResponseItem::Message requires {role, content:[{type,text}]}.
			writeEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done",
				"item": map[string]any{
					"type":    "message",
					"id":      curItemID,
					"role":    "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": textBuf.String()}},
				},
			})
			textBuf.Reset()
		}
		curItemID = ""
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
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
		case "message_start":
			if m, _ := msg["message"].(map[string]any); m != nil {
				if id, _ := m["id"].(string); id != "" {
					respID = id
				}
				if mdl, _ := m["model"].(string); mdl != "" {
					respModel = mdl
				}
			}
			emitCreated()
		case "content_block_start":
			emitCreated() // some gateways omit message_start; ensure created fires before any item

			closeCurrent()
			block, _ := msg["content_block"].(map[string]any)
			switch block["type"] {
			case "tool_use":
				id, _ := block["id"].(string)
				if id == "" {
					id = newID("fc")
				}
				name, _ := block["name"].(string)
				curItemID = id
				curToolID = id
				curToolName = name
				writeEvent("response.output_item.added", map[string]any{
					"type": "response.output_item.added",
					"item": map[string]any{
						"type":      "function_call",
						"id":        id,
						"call_id":   id,
						"name":      name,
						"arguments": "",
					},
				})
			case "text", "":
				curItemID = newID("msg")
				writeEvent("response.output_item.added", map[string]any{
					"type": "response.output_item.added",
					"item": map[string]any{
						"type":    "message",
						"id":      curItemID,
						"role":    "assistant",
						"content": []any{},
					},
				})
			case "thinking":
				// Accumulate thinking text + signature; emit a reasoning output
				// item at content_block_stop. Do not open a message/tool item.
				thinkingBuf.Reset()
				thinkingSig = ""
				if sig, _ := block["signature"].(string); sig != "" {
					thinkingSig = sig
				}
			default:
				// server-tool / other deferred block types: ignore without
				// opening an output item.
			}
		case "content_block_delta":
			delta, _ := msg["delta"].(map[string]any)
			// Thinking deltas do not belong to the open message/tool item.
			switch delta["type"] {
			case "thinking_delta":
				if t, _ := delta["thinking"].(string); t != "" {
					thinkingBuf.WriteString(t)
				}
				continue
			case "signature_delta":
				if sig, _ := delta["signature"].(string); sig != "" {
					thinkingSig += sig
				}
				continue
			}
			if curItemID == "" {
				continue
			}
			switch delta["type"] {
			case "text_delta":
				if text, _ := delta["text"].(string); text != "" {
					textBuf.WriteString(text)
					writeEvent("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": curItemID, "delta": text})
				}
			case "input_json_delta":
				if pj, _ := delta["partial_json"].(string); pj != "" {
					argsBuf.WriteString(pj)
					writeEvent("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": curItemID, "delta": pj})
				}
			}
		case "content_block_stop":
			// If the block just closed was a thinking block, emit a reasoning
			// item carrying the signature as encrypted_content so Codex echoes
			// it back next turn (mapped to the Anthropic `signature` field by
			// the request converter). Without this round-trip, Anthropic rejects
			// tool-use loops that interleave thinking with a 400.
			if thinkingSig != "" {
				item := map[string]any{
					"type":              "reasoning",
					"encrypted_content": thinkingSig,
				}
				if thinkingBuf.Len() > 0 {
					item["content"] = []any{map[string]any{
						"type": "reasoning_text", "text": thinkingBuf.String(),
					}}
				}
				writeEvent("response.output_item.added", map[string]any{
					"type": "response.output_item.added",
					"item": item,
				})
				writeEvent("response.output_item.done", map[string]any{
					"type": "response.output_item.done",
					"item": item,
				})
			}
			thinkingBuf.Reset()
			thinkingSig = ""
			closeCurrent()
		case "message_stop":
			closeCurrent()
		}
	}
	closeCurrent()
	emitCreated() // edge case: empty stream — still emit a well-formed pair
	writeEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     respID,
			"model":  respModel,
			"status": "completed",
		},
	})
	return scanner.Err()
}
