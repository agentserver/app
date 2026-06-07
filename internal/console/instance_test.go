package console

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDiscoverInstanceUsesHealthyPortFile(t *testing.T) {
	requests := make(chan healthRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- healthRequest{Method: r.Method, Path: r.URL.Path}
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
	req := receiveHealthRequest(t, requests)
	if req.Method != http.MethodGet {
		t.Fatalf("method=%s", req.Method)
	}
	if req.Path != "/api/console/health" {
		t.Fatalf("path=%s", req.Path)
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

func TestDiscoverInstanceRejectsUnexpectedHealthPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"starting"}`))
	}))
	defer srv.Close()
	path := writeInstanceForServer(t, srv)

	if _, ok := DiscoverInstance(context.Background(), path); ok {
		t.Fatal("unexpected health payload should not be healthy")
	}
	assertPortFileRemoved(t, path)
}

func TestDiscoverInstanceRejectsRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/console/health" {
			http.Redirect(w, r, "/healthy", http.StatusTemporaryRedirect)
			return
		}
		w.Write([]byte(`{"state":"ok"}`))
	}))
	defer srv.Close()
	path := writeInstanceForServer(t, srv)

	if _, ok := DiscoverInstance(context.Background(), path); ok {
		t.Fatal("redirected health response should not be healthy")
	}
	assertPortFileRemoved(t, path)
}

func TestDiscoverInstanceKeepsPortFileWhenContextCanceled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console-port.json")
	if err := WriteInstanceInfo(path, InstanceInfo{Port: 1, PID: 123}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, ok := DiscoverInstance(ctx, path); ok {
		t.Fatal("canceled discovery should not be healthy")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("port file should remain after canceled discovery, err=%v", err)
	}
}

func TestWriteInstanceInfoPublishesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console-port.json")
	if err := WriteInstanceInfo(path, InstanceInfo{Port: 4321, PID: 123}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got InstanceInfo
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("port file JSON is invalid: %v", err)
	}
	if got.Port != 4321 || got.PID != 123 {
		t.Fatalf("got %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.StartedAt); err != nil {
		t.Fatalf("StartedAt=%q err=%v", got.StartedAt, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode=%#o", got)
	}
	assertOnlyPathInDir(t, dir, "console-port.json")
}

func TestWriteInstanceInfoCleansTempFileOnRenameError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console-port.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteInstanceInfo(path, InstanceInfo{Port: 4321, PID: 123}); err == nil {
		t.Fatal("expected write error")
	}
	assertOnlyPathInDir(t, dir, "console-port.json")
}

type healthRequest struct {
	Method string
	Path   string
}

func receiveHealthRequest(t *testing.T, requests <-chan healthRequest) healthRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for health request")
		return healthRequest{}
	}
}

func serverPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, portRaw, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func writeInstanceForServer(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "console-port.json")
	if err := WriteInstanceInfo(path, InstanceInfo{Port: serverPort(t, srv.URL), PID: 123}); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPortFileRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("port file should be removed, err=%v", err)
	}
}

func assertOnlyPathInDir(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("unexpected directory entries: %+v", entries)
	}
}
