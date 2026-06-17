# GLM / DeepSeek Codex Support Design

## Goal

Let Codex Desktop (and the Codex CLI / Linux headless frontends that share `~/.codex/config.toml`) use `glm-5.2[1m]` and `deepseek-v4-pro` in addition to `gpt-5.5`, all through the existing local proxy and the same ModelServer account (`code.ai.cs.ac.cn`, same key as `gpt-5.5`).

## Background & Research

Codex only speaks the **OpenAI Responses API** (`POST /v1/responses`) — since v0.81.0 it dropped `wire_api = "chat"`. GLM and DeepSeek are not Responses-native on the gateway:

- **`gpt-5.5`** already works via `code.ai.cs.ac.cn/v1/responses` (pass-through).
- **`deepseek-v4-pro`** is served via **Chat Completions** (`/v1/chat/completions`).
- **`glm-5.2[1m]`** is served via **Anthropic Messages** (`/v1/messages`).

The sibling **`opencode-desktop-support`** feature (merged on `origin/master`, PR #10) already established this exact 3-bucket routing in `internal/opencode/config.go` (`responsesModels=[gpt-5.5]`, `compatibleModels=[deepseek-v4-pro]`, `anthropicModels=[glm-5.1]`) — but **opencode does the protocol adaptation itself** through its `@ai-sdk/openai`, `@ai-sdk/openai-compatible`, and `@ai-sdk/anthropic` SDK providers, so its local proxy just forwards each path unchanged. **Codex has no such SDK layer** — it always emits Responses — so Codex + GLM/DeepSeek needs the *proxy* to convert. That conversion is the subject of this design.

Reference converters studied (read-only, not embedded): `farion1231/cc-switch` (Rust/Tauri, accumulates the edge cases), `responses-proxy` (Rust crate, SSE + reasoning), `musistudio/claude-code-router`, and llama-swap's Go `proxymanager.go` (structural reference).

## Locked Decisions

1. **Base:** this feature builds on `origin/master` (which contains `opencode-desktop-support`). The earlier local checkout was 22 commits behind; the spec is re-grounded on the real current code.
2. **Where conversion lives:** extend the existing `internal/modelproxy` (single process, `127.0.0.1:53452`). No second resident service.
3. **Two converters, per-model, aligned with opencode's proven gateway routing:**
   - `gpt-5.5` → pass-through `/v1/responses` (unchanged).
   - `deepseek-v4-pro` → **Responses → Chat Completions** → `POST /v1/chat/completions`.
   - `glm-5.2[1m]` → **Responses → Anthropic Messages** → `POST /v1/messages` (the proxy already forwards this path and sets `X-Api-Key`).
4. **Routing:** per-request, by reading the `model` field from the request body. No proxy restart on model switch.
5. **Model selection UX:** two layers.
   - **Default model** (new sessions): the `model` field in `~/.codex/config.toml`, set via `agentctl set-model <name>` (Windows desktop) and `agentserver set-model <name>` (Linux headless), or by editing `config.toml`.
   - **Live switch:** Codex's `/model` picklist is populated by the gateway's `GET /v1/models` (pass-through-proxied); selecting a name just sets the `model` string Codex sends, routed by #4 with no restart.
6. **Unknown model / unsupported feature:** fall back to pass-through `/v1/responses` **and log**, rather than hard-fail (preserves `gpt-5.5` parity as the safe default).
7. **Conversion implementation:** self-developed in Go (`internal/protoconv`), using the open-source converters as reference implementations (read-only, not runtime deps). **Delivered incrementally:** v1 ships text + tool calls + streaming (the core coding loop) for both converters; file/audio/reasoning parity is a known v1 limitation, structurally designed-for and filled in by a follow-up.
8. **Catalog is table-driven and mirrors opencode's buckets:** adding models = adding rows. (A future cleanup may extract a shared `internal/modelcatalog` used by both opencode and protoconv; out of scope for v1 — we don't touch opencode's working code.)

## Assumptions (confirmed in brainstorm)

- Scope covers all frontends sharing `~/.codex/config.toml`: Codex Desktop, Codex CLI, Linux headless `agentserver`.
- One upstream (`code.ai.cs.ac.cn`) and one key (the ModelServer token the proxy already injects).
- `gpt-5.5` stays pass-through Responses.
- Upstream model names are passed through as-is (`glm-5.2[1m]`, `deepseek-v4-pro`).
- Conversion correctness is validated end-to-end on the Windows test box (`ssh Administrator@9.0.16.110`) against real Codex.

## Architecture

```
Codex Desktop / CLI / headless
   │  POST /v1/responses  (Authorization: Bearer <local-proxy-token> | X-Api-Key)
   ▼
internal/modelproxy.NewHandler  (127.0.0.1:53452)
   │  existing: validate local token, inject ModelServer bearer, /v1/messages → +X-Api-Key,
   │            normalizeResponsesInstructions (reads/rewrites body)
   │  NEW: peek `model` from body, dispatch via protoconv
   │
   ├── model == gpt-5.5  ───────────────►  pass-through reverse proxy → /v1/responses  (unchanged)
   │
   ├── model == deepseek-v4-pro ─►  protoconv.Chat   Responses⇄ChatCompletions  → /v1/chat/completions
   │
   ├── model == glm-5.2[1m]  ────►  protoconv.Anthropic  Responses⇄AnthropicMessages → /v1/messages
   │
   └── unknown ─────────────────────►  pass-through /v1/responses + log  (safe fallback)
```

## Components

- **`internal/protoconv` (new, pure):** two translators plus shared Responses types. Pure request/response mappers + streaming SSE adapters; no network code, unit-testable in isolation.
  - `chat.go` — Responses ⇄ OpenAI Chat Completions (deepseek).
  - `anthropic.go` — Responses ⇄ Anthropic Messages (glm).
  - `responses.go` — shared Responses request/response + SSE event types.
  - `catalog.go` — the model→{converter, upstream path} table used by routing and `set-model` validation.
- **`internal/modelproxy` (extended):** `NewHandler` gains a routing step before the reverse proxy. Reuses the existing local-token auth (`validLocalRequestToken`: Bearer or `X-Api-Key`), ModelServer token injection, `/v1/messages` `X-Api-Key` handling, hop-by-hop stripping, body-size limits, and the instructions-normalization hook. The converter output replaces the request body/path; for pass-through models the existing reverse proxy runs unchanged.
- **`internal/codex` (extended):** `ModelserverSettings()` keeps returning the `gpt-5.5` default for back-compat; callers that select another model override `s.Model` before `UpdateConfig` (the model already flows into `config.toml`'s `model` field via the existing merge).
- **CLI:** `agentctl set-model <name>` and `agentserver set-model <name>` — thin subcommands that validate `<name>` against the protoconv catalog and rewrite the `model` field in `~/.codex/config.toml` via the existing merge + backup logic. No daemon restart required.

## Data Flow

1. Codex sends `POST /v1/responses` with `model` set (e.g. `glm-5.2[1m]`) and the local proxy token.
2. Proxy validates the local token, strips hop-by-hop headers, injects the current ModelServer bearer token (and `X-Api-Key` if the eventual upstream is `/v1/messages`).
3. Proxy peeks `model`:
   - `gpt-5.5` → reverse-proxy to `/v1/responses` unchanged.
   - `deepseek-v4-pro` → `protoconv.Chat` translates the body to a Chat Completions request; proxy POSTs to `code.ai.cs.ac.cn/v1/chat/completions`; the response/SSE is translated back to Responses shape.
   - `glm-5.2[1m]` → `protoconv.Anthropic` translates the body to an Anthropic Messages request; proxy POSTs to `code.ai.cs.ac.cn/v1/messages`; the response/SSE is translated back to Responses shape.
   - unknown → pass-through `/v1/responses` and log.
4. Codex receives a Responses-shaped response it can render. Switching models = change the `model` field; the next request routes accordingly with no restart.

## Conversion Contract

Implemented in Go (`internal/protoconv`), referencing cc-switch / `responses-proxy` / llama-swap for edge cases. Built incrementally — request/response mapping for **text + tool calls** first, then streaming; file/audio/reasoning parity deferred (decision #7) but the types admit them.

### Converter A — Responses ⇄ Chat Completions (deepseek-v4-pro)

Request (Responses → Chat Completions):

- `instructions` → prepended `system` message.
- `input` items → `messages`:
  - `message` → `{role, content}`; flatten `content[].text` / `output_text` parts to a string.
  - `function_call` → assistant message with `tool_calls:[{id:call_id,type:"function",function:{name,arguments}}]`.
  - `function_call_output` → `{role:"tool", tool_call_id:call_id, content:output}`.
  - `reasoning` → carried into `reasoning_content` where supported, else dropped.
- `tools[]` (function) → `tools:[{type:"function",function:{name,description,parameters}}]`; non-function tool types logged and dropped.
- `reasoning.effort` / `reasoning.summary` → forwarded where accepted, ignored otherwise. `stream` honored.

Response (Chat Completions → Responses): non-stream assembles `{id,model,status,output:[message+function_call items],usage}`; stream consumes `data:{choices:[{delta,finish_reason}]}`…`[DONE]` and emits `response.created` → `output_item.added` → `output_text.delta` / `function_call_arguments.delta` (buffered per `tool_call_id`) → `output_item.done` → `response.completed`.

### Converter B — Responses ⇄ Anthropic Messages (glm-5.2[1m])

Request (Responses → Anthropic Messages):

- `instructions` + any system/developer input messages → Anthropic top-level `system` field.
- `input` items → Anthropic `messages` (content-block based):
  - `message` → `{role, content:[{type:"text", text}]}`.
  - `function_call` → assistant message with `content:[{type:"tool_use", id, name, input}]`.
  - `function_call_output` → `{role:"user", content:[{type:"tool_result", tool_use_id, content}]}`.
  - `reasoning` → `{type:"thinking",...}` where supported, else dropped.
- `tools[]` (function) → `tools:[{name,description,input_schema}]`.
- `reasoning.effort` → Anthropic `thinking:{type:"enabled"}` mapping where applicable. `stream` honored.

Response (Anthropic Messages → Responses): non-stream maps Anthropic content blocks to Responses `output` items (`text` → message item, `tool_use` → function_call item). Stream consumes Anthropic events (`message_start`, `content_block_start`, `content_block_delta` of `text_delta`/`input_json_delta`, `content_block_stop`, `message_delta`, `message_stop`) and emits the same Responses event sequence as Converter A.

## Error Handling

- Upstream non-2xx: surface the gateway error body to Codex as a Responses-shaped error so Codex shows a sensible message (mirror cc-switch's error-body translation for known gateway quirks).
- Malformed request body: `400` with a clear error string.
- Unknown/unsupported Responses feature: pass through unconverted to `/v1/responses` and log (safe fallback).
- Per-request `recover`: a conversion panic is caught so one bad request can't kill the proxy; the request returns a `502`-style Responses error.

## Testing

- `protoconv` focused unit tests (table-driven): both converters' request mappers (message / function_call / function_call_output / reasoning items, tool conversion), response assemblers, and streaming translators fed canned upstream chunks asserting the exact Responses event sequence (Chat Completions chunks for A; Anthropic events for B).
- `modelproxy` handler test: three routing cases (`gpt-5.5` pass-through, `deepseek-v4-pro` → chat converter, `glm-5.2[1m]` → anthropic converter) against `httptest` fake upstreams, plus the unknown-model fallback, reusing the harness in `proxy_test.go`.
- `codex` config test: existing `gpt-5.5` cases unchanged; add a case that overrides `s.Model` and asserts it lands in `config.toml`.
- `set-model`: validation against the catalog (accept the three known models, reject unknown) and that it rewrites only the `model` field while preserving other keys.
- End-to-end smoke against real Codex on the Windows test box (`ssh Administrator@9.0.16.110`) is the manual acceptance check (requires a real ModelServer key).

## Risks & Open Items

- **Streaming correctness is the main risk, doubled across two converters.** Mitigated by (a) non-stream first, (b) referencing cc-switch / llama-swap's event sequencing and test vectors, (c) validating the exact sequence with the real Codex client. Converter B (Anthropic content-block streaming) is the harder of the two.
- **Scope intentionally bounded for v1:** file/audio content parts and full reasoning-content parity are deferred (#7) — text + tool calls + streaming covers the core Codex coding loop.
- **Reasoning parity:** DeepSeek/GLM reasoning fields differ across providers; carry reasoning where supported, degrade gracefully otherwise.
- The gateway's exact `/v1/chat/completions` and `/v1/messages` error shapes will be confirmed against real traffic during implementation.

## Out of Scope / Future

- A shared `internal/modelcatalog` unified across opencode + protoconv (don't touch opencode's working code in v1).
- More GLM/DeepSeek variants beyond the three released models.
- File/audio/reasoning parity beyond the v1 text+tools+streaming core.
- A model-picker in the tray/console UI (the catalog + `set-model` CLI are the v1 surface).
