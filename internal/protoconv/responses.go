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
