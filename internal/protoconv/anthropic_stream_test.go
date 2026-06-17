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
