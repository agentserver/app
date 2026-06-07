package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type InstanceInfo struct {
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at,omitempty"`
}

func WriteInstanceInfo(path string, info InstanceInfo) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info.StartedAt == "" {
		info.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func DiscoverInstance(ctx context.Context, path string) (InstanceInfo, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return InstanceInfo{}, false
	}
	var info InstanceInfo
	if err := json.Unmarshal(b, &info); err != nil || info.Port <= 0 {
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/console/health", info.Port), nil)
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode/100 != 2 {
		if resp != nil {
			resp.Body.Close()
		}
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	resp.Body.Close()
	return info, true
}
