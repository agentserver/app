package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/modelproxy"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

func TestRunWithDepsServesLocalModelProxy(t *testing.T) {
	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	if err := sec.Set(tokenrefresh.AccessTokenKey, "access-from-secret"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set(tokenrefresh.RefreshTokenKey, "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if err := sec.Set(tokenrefresh.AccessTokenExpiresAtKey, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	gotAuth := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	addr := freeTCPAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	refreshCalled := make(chan struct{}, 1)
	go func() {
		errCh <- runWithDeps(ctx, runDeps{
			Secrets:              sec,
			OAuth:                oauth.AuthCodeConfig{ClientID: "client-x"},
			LocalProxyToken:      "random-local-token",
			ProxyAddr:            addr,
			ProxyUpstreamBaseURL: upstream.URL + "/v1",
			Refresh: func(context.Context, oauth.AuthCodeConfig, string) (oauth.Token, error) {
				select {
				case refreshCalled <- struct{}{}:
				default:
				}
				return oauth.Token{AccessToken: "refreshed-access", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
			},
		})
	}()

	waitForProxyHealth(t, addr)
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer random-local-token")
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

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runWithDeps returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithDeps did not stop after context cancellation")
	}
	select {
	case <-refreshCalled:
		t.Fatal("refresh should not run while stored token expires in the future")
	default:
	}
}

func TestRunWithDepsReturnsWhenDaemonLockAlreadyHeld(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "token-refresher.lock")
	lock, err := tokenrefresh.AcquireDaemonLock(lockPath)
	if err != nil {
		t.Fatalf("AcquireDaemonLock: %v", err)
	}
	defer lock.Close()

	err = runWithDeps(context.Background(), runDeps{
		Secrets:  secrets.New(filepath.Join(t.TempDir(), "secrets.json")),
		LockPath: lockPath,
	})
	if !errors.Is(err, tokenrefresh.ErrDaemonAlreadyRunning) {
		t.Fatalf("runWithDeps err=%v, want %v", err, tokenrefresh.ErrDaemonAlreadyRunning)
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

func waitForProxyHealth(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + modelproxy.HealthPath)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("model proxy did not become healthy at %s", addr)
}
