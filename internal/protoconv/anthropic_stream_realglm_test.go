package protoconv

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Real GLM /v1/messages stream captured from the gateway.
const realGLMSSE = "event: message_start\ndata: {\"type\": \"message_start\", \"message\": {\"id\": \"msg_x\", \"type\": \"message\", \"role\": \"assistant\", \"model\": \"glm-5.2\", \"content\": [], \"stop_reason\": null, \"stop_sequence\": null, \"usage\": {\"input_tokens\": 0, \"output_tokens\": 0}}}\n\n" +
	"event: ping\ndata: {\"type\": \"ping\"}\n\n" +
	"event: content_block_start\ndata: {\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"OK\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\": \"content_block_stop\", \"index\": 0}\n\n" +
	"event: message_delta\ndata: {\"type\": \"message_delta\", \"delta\": {\"stop_reason\": \"end_turn\", \"stop_sequence\": null}, \"usage\": {\"input_tokens\": 12, \"output_tokens\": 2}}\n\n" +
	"event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"

func TestAnthropicStreamRealGLM(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteAnthropicStreamAsResponses(strings.NewReader(realGLMSSE), rec); err != nil {
		t.Fatalf("err: %v", err)
	}
	body := rec.Body.String()
	t.Logf("OUTPUT:\n%s", body)
	if !strings.Contains(body, "output_text.delta") {
		t.Errorf("MISSING output_text.delta")
	}
	if !strings.Contains(body, `"OK"`) {
		t.Errorf("MISSING the OK text delta")
	}
}
