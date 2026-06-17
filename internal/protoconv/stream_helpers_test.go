package protoconv

import (
	"encoding/json"
	"strings"
)

type sseFrame struct{ event, data string }

func parseSSE(body string) []sseFrame {
	var frames []sseFrame
	var cur sseFrame
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			if cur.event != "" || cur.data != "" {
				frames = append(frames, cur)
				cur = sseFrame{}
			}
			continue
		}
		if strings.HasPrefix(l, "event:") {
			cur.event = strings.TrimSpace(strings.TrimPrefix(l, "event:"))
		} else if strings.HasPrefix(l, "data:") {
			cur.data = strings.TrimSpace(strings.TrimPrefix(l, "data:"))
		}
	}
	if cur.event != "" || cur.data != "" {
		frames = append(frames, cur)
	}
	return frames
}

// countItemEvents returns (#added, #done, #function_call_arguments.delta with item_id set).
func countItemEvents(body string) (added, done, argWithItem int) {
	for _, f := range parseSSE(body) {
		switch f.event {
		case "response.output_item.added":
			added++
		case "response.output_item.done":
			done++
		case "response.function_call_arguments.delta":
			var d map[string]any
			if json.Unmarshal([]byte(f.data), &d) == nil {
				if _, ok := d["item_id"].(string); ok && d["item_id"] != "" {
					argWithItem++
				}
			}
		}
	}
	return
}
