package modelaccess

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

func TestRunDaemonServesLocalModelProxyAndKeepsServingWithoutRefreshToken(t *testing.T) {
	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access-from-secret"); err != nil {
		t.Fatal(err)
	}

	gotAuth := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	addr := freeTCPAddr(t)
	lockPath := filepath.Join(dir, "model-proxy.lock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunDaemon(ctx, DaemonOptions{
			Secrets:              sec,
			OAuth:                oauth.AuthCodeConfig{ClientID: "client-x"},
			ProxyAddr:            addr,
			ProxyUpstreamBaseURL: upstream.URL + "/v1",
			LockPath:             lockPath,
		})
	}()

	waitForProxyHealth(t, "http://"+addr)
	lock, err := tokenrefresh.AcquireDaemonLock(lockPath)
	if !errors.Is(err, tokenrefresh.ErrDaemonAlreadyRunning) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatalf("AcquireDaemonLock err=%v, want %v", err, tokenrefresh.ErrDaemonAlreadyRunning)
	}

	resp, err := http.Get("http://" + addr + "/v1/models")
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	select {
	case got := <-gotAuth:
		if got != "Bearer access-from-secret" {
			t.Fatalf("Authorization = %q, want bearer access token", got)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive proxied request")
	}
	select {
	case err := <-errCh:
		t.Fatalf("RunDaemon returned before cancellation: %v", err)
	default:
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunDaemon returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunDaemon did not stop after context cancellation")
	}
}

func TestEnsureDaemonStartsAgentserverModelProxyDaemonWhenHealthMissing(t *testing.T) {
	var started bool
	var gotArgs []string
	healthy := false
	err := EnsureDaemon(context.Background(), EnsureDaemonOptions{
		ExePath:      "/opt/agentserver/agentserver",
		ProxyBaseURL: "http://127.0.0.1:1",
		HealthCheck: func(context.Context, string) bool {
			return healthy
		},
		StartProcess: func(cmd *exec.Cmd) error {
			started = true
			gotArgs = append([]string(nil), cmd.Args...)
			healthy = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureDaemon returned %v", err)
	}
	if !started {
		t.Fatal("StartProcess was not called")
	}
	wantArgs := []string{"/opt/agentserver/agentserver", "model-proxy-daemon"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("cmd args = %#v, want %#v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("cmd args = %#v, want %#v", gotArgs, wantArgs)
		}
	}
}

func TestEnsureDaemonReusesHealthyExistingProxy(t *testing.T) {
	started := false
	err := EnsureDaemon(context.Background(), EnsureDaemonOptions{
		ExePath:      "/opt/agentserver/agentserver",
		ProxyBaseURL: "http://127.0.0.1:1",
		HealthCheck: func(context.Context, string) bool {
			return true
		},
		StartProcess: func(*exec.Cmd) error {
			started = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureDaemon returned %v", err)
	}
	if started {
		t.Fatal("StartProcess was called for healthy proxy")
	}
}

func TestEnsureDaemonReportsProxyUnavailableWhenPortOccupied(t *testing.T) {
	err := EnsureDaemon(context.Background(), EnsureDaemonOptions{
		ExePath:      "/opt/agentserver/agentserver",
		ProxyBaseURL: "http://127.0.0.1:1",
		HealthCheck: func(context.Context, string) bool {
			return false
		},
		StartProcess: func(*exec.Cmd) error {
			return errors.New("listen tcp 127.0.0.1:53452: bind: address already in use")
		},
	})
	if !errors.Is(err, ErrProxyUnavailable) {
		t.Fatalf("EnsureDaemon err=%v, want %v", err, ErrProxyUnavailable)
	}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func waitForProxyHealth(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ProxyHealthy(context.Background(), baseURL) {
			return
		}
		resp, err := http.Get(baseURL + modelproxy.HealthPath)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("model proxy did not become healthy at %s", baseURL)
}
