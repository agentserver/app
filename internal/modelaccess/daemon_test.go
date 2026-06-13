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

	"github.com/agentserver/agentserver-pkg/internal/codex"
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

	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+codex.LocalProxyAPIKeyValue)
	resp, err := http.DefaultClient.Do(req)
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

func TestRunDaemonStopsRefreshWhenProxyStartupFails(t *testing.T) {
	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access-from-secret"); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	refreshStarted := make(chan struct{})
	refreshStopped := make(chan struct{})
	overrideRunTokenRefresh(t, func(ctx context.Context, _ tokenrefresh.Options) error {
		close(refreshStarted)
		<-ctx.Done()
		close(refreshStopped)
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = RunDaemon(ctx, DaemonOptions{
		Secrets:   sec,
		OAuth:     oauth.AuthCodeConfig{ClientID: "client-x"},
		ProxyAddr: ln.Addr().String(),
	})
	if err == nil {
		t.Fatal("RunDaemon returned nil, want proxy startup error")
	}
	select {
	case <-refreshStarted:
	default:
		cancel()
		t.Fatal("refresh did not start")
	}
	select {
	case <-refreshStopped:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("refresh was still running after proxy startup failure")
	}
}

func TestRunDaemonStopsProxyWhenRefreshReturnsHardError(t *testing.T) {
	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access-from-secret"); err != nil {
		t.Fatal(err)
	}

	addr := freeTCPAddr(t)
	refreshErr := errors.New("refresh failed hard")
	overrideRunTokenRefresh(t, func(ctx context.Context, _ tokenrefresh.Options) error {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if ProxyHealthy(ctx, "http://"+addr) {
				return refreshErr
			}
			time.Sleep(10 * time.Millisecond)
		}
		return errors.New("proxy never became healthy")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := RunDaemon(ctx, DaemonOptions{
		Secrets:   sec,
		OAuth:     oauth.AuthCodeConfig{ClientID: "client-x"},
		ProxyAddr: addr,
	})
	if !errors.Is(err, refreshErr) {
		t.Fatalf("RunDaemon err=%v, want %v", err, refreshErr)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !ProxyHealthy(context.Background(), "http://"+addr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t.Fatal("proxy was still healthy after refresh hard error")
}

func TestRunDaemonReturnsNilWhenDeadlineExpiresFromRefreshBranch(t *testing.T) {
	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access-from-secret"); err != nil {
		t.Fatal(err)
	}

	overrideRunTokenRefresh(t, func(ctx context.Context, _ tokenrefresh.Options) error {
		<-ctx.Done()
		return ctx.Err()
	})
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		time.Sleep(time.Millisecond)
		err := RunDaemon(ctx, DaemonOptions{
			Secrets:   sec,
			OAuth:     oauth.AuthCodeConfig{ClientID: "client-x"},
			ProxyAddr: freeTCPAddr(t),
		})
		cancel()
		if err != nil {
			t.Fatalf("RunDaemon err=%v on iteration %d, want nil", err, i)
		}
	}
}

func TestCleanDaemonShutdownRecognizesActiveContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if !cleanDaemonShutdown(ctx, context.DeadlineExceeded) {
		t.Fatal("context deadline exceeded was not treated as clean shutdown")
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

func TestEnsureDaemonRequiresExePath(t *testing.T) {
	err := EnsureDaemon(context.Background(), EnsureDaemonOptions{
		ProxyBaseURL: "http://127.0.0.1:1",
		HealthCheck: func(context.Context, string) bool {
			return false
		},
		StartProcess: func(*exec.Cmd) error {
			t.Fatal("StartProcess should not be called without ExePath")
			return nil
		},
	})
	if err == nil || err.Error() != "agentserver executable path required" {
		t.Fatalf("EnsureDaemon err=%v, want agentserver executable path required", err)
	}
}

func TestEnsureDaemonBuildsDetachedCommandNotBoundToCallerContext(t *testing.T) {
	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calledCommandFactory := false
	overrideDaemonCommand(t, func(exePath string, args ...string) *exec.Cmd {
		calledCommandFactory = true
		return exec.Command(exePath, args...)
	})

	healthy := false
	err := EnsureDaemon(parentCtx, EnsureDaemonOptions{
		ExePath:      "/opt/agentserver/agentserver",
		ProxyBaseURL: "http://127.0.0.1:1",
		HealthCheck: func(context.Context, string) bool {
			return healthy
		},
		StartProcess: func(*exec.Cmd) error {
			healthy = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureDaemon returned %v", err)
	}
	if !calledCommandFactory {
		t.Fatal("daemon command factory was not called")
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

func TestProxyHealthyReturnsFalseWhenServerDoesNotRespond(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	connCh := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		connCh <- conn
	}()

	done := make(chan bool, 1)
	go func() {
		done <- ProxyHealthy(context.Background(), "http://"+ln.Addr().String())
	}()

	var conn net.Conn
	select {
	case conn = <-connCh:
		defer conn.Close()
	case err := <-acceptErr:
		t.Fatalf("Accept: %v", err)
	case <-time.After(time.Second):
		t.Fatal("ProxyHealthy did not connect to listener")
	}

	select {
	case healthy := <-done:
		if healthy {
			t.Fatal("ProxyHealthy returned true for non-responding server")
		}
	case <-time.After(time.Second):
		t.Fatal("ProxyHealthy did not time out")
	}
}

func TestProxyHealthyChecksRootHealthPathWhenBaseURLHasPath(t *testing.T) {
	gotPath := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath <- r.URL.Path
		if r.URL.Path != modelproxy.HealthPath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if !ProxyHealthy(context.Background(), server.URL+"/v1") {
		t.Fatal("ProxyHealthy returned false")
	}
	select {
	case got := <-gotPath:
		if got != modelproxy.HealthPath {
			t.Fatalf("health path = %q, want %q", got, modelproxy.HealthPath)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive health check")
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

func overrideRunTokenRefresh(t *testing.T, fn func(context.Context, tokenrefresh.Options) error) {
	t.Helper()
	original := runTokenRefresh
	runTokenRefresh = fn
	t.Cleanup(func() {
		runTokenRefresh = original
	})
}

func overrideDaemonCommand(t *testing.T, fn func(string, ...string) *exec.Cmd) {
	t.Helper()
	original := newDaemonCommand
	newDaemonCommand = fn
	t.Cleanup(func() {
		newDaemonCommand = original
	})
}
