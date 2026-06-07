package console

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
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
	client := http.Client{
		Timeout: 500 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		if ctx.Err() != nil {
			return InstanceInfo{}, false
		}
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	var health struct {
		State string `json:"state"`
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return InstanceInfo{}, false
		}
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	if json.Unmarshal(body, &health) != nil || health.State != "ok" {
		_ = os.Remove(path)
		return InstanceInfo{}, false
	}
	return info, true
}
