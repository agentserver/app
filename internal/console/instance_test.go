package console

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverInstanceUsesHealthyPortFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/console/health" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Write([]byte(`{"state":"ok"}`))
	}))
	defer srv.Close()
	port := serverPort(t, srv.URL)
	dir := t.TempDir()
	path := filepath.Join(dir, "console-port.json")
	if err := WriteInstanceInfo(path, InstanceInfo{Port: port, PID: 123}); err != nil {
		t.Fatal(err)
	}
	got, ok := DiscoverInstance(context.Background(), path)
	if !ok || got.Port != port {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestDiscoverInstanceDeletesStalePortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console-port.json")
	if err := WriteInstanceInfo(path, InstanceInfo{Port: 1, PID: 123}); err != nil {
		t.Fatal(err)
	}
	if _, ok := DiscoverInstance(context.Background(), path); ok {
		t.Fatal("stale instance should not be healthy")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale port file should be removed, err=%v", err)
	}
}

func serverPort(t *testing.T, raw string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(raw, "http://127.0.0.1:%d", &port); err != nil {
		t.Fatal(err)
	}
	return port
}
