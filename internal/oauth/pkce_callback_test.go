package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReservePort_FirstFree(t *testing.T) {
	// Bind two arbitrary low ports first to occupy them, then offer
	// ReservePort a list where the first two are taken.
	occupy := func() (int, net.Listener) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return l.Addr().(*net.TCPAddr).Port, l
	}
	p1, l1 := occupy()
	defer l1.Close()
	p2, l2 := occupy()
	defer l2.Close()

	// Find a third port that's currently free, then close it so it can be re-bound.
	l3, _ := net.Listen("tcp", "127.0.0.1:0")
	p3 := l3.Addr().(*net.TCPAddr).Port
	l3.Close()

	cfg := AuthCodeConfig{Ports: []int{p1, p2, p3}}
	got, ln, err := ReservePort(cfg)
	if err != nil {
		t.Fatalf("ReservePort: %v", err)
	}
	defer ln.Close()
	if got != p3 {
		t.Errorf("got port %d, want %d (p1=%d occupied, p2=%d occupied)", got, p3, p1, p2)
	}
}

func TestReservePort_AllBusy(t *testing.T) {
	l1, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l1.Close()
	p1 := l1.Addr().(*net.TCPAddr).Port

	cfg := AuthCodeConfig{Ports: []int{p1}}
	_, _, err := ReservePort(cfg)
	if !errors.Is(err, ErrAllPortsBusy) {
		t.Errorf("got %v, want ErrAllPortsBusy", err)
	}
}

// Helper for later tests that need a known-free port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestCallbackPages_AllPresent(t *testing.T) {
	for _, name := range []string{"success", "denied", "state_mismatch", "missing_code"} {
		b := callbackPage(name)
		if len(b) == 0 {
			t.Errorf("page %s is empty", name)
		}
		if !strings.Contains(string(b), "<html") {
			t.Errorf("page %s is not HTML", name)
		}
	}
}

func TestStartListening_Success(t *testing.T) {
	cfg := AuthCodeConfig{
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{freePort(t)},
		LoginTimeout: 2 * time.Second,
	}
	port, ln, err := ReservePort(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, shutdown := StartListening(ctx, ln, cfg, "state-x")
	defer shutdown()

	url := fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback?code=foo&state=state-x", port)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "登录成功") {
		t.Errorf("body did not contain success page: %s", body)
	}

	select {
	case res := <-ch:
		if res.Code != "foo" || res.State != "state-x" || res.Error != "" {
			t.Errorf("result = %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not receive")
	}
}

func TestStartListening_OAuthError(t *testing.T) {
	cfg := AuthCodeConfig{
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{freePort(t)},
		LoginTimeout: time.Second,
	}
	port, ln, err := ReservePort(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, shutdown := StartListening(ctx, ln, cfg, "state-y")
	defer shutdown()

	resp, _ := http.Get(fmt.Sprintf(
		"http://127.0.0.1:%d/oauth/modelserver/callback?error=access_denied&error_description=user+refused",
		port))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "登录被拒绝") {
		t.Errorf("body did not contain denied page: %s", body)
	}
	select {
	case res := <-ch:
		if res.Error != "access_denied" {
			t.Errorf("result = %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not receive")
	}
}

func TestStartListening_StateMismatch(t *testing.T) {
	cfg := AuthCodeConfig{
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{freePort(t)},
		LoginTimeout: 500 * time.Millisecond,
	}
	port, ln, err := ReservePort(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, shutdown := StartListening(ctx, ln, cfg, "state-correct")
	defer shutdown()

	resp, _ := http.Get(fmt.Sprintf(
		"http://127.0.0.1:%d/oauth/modelserver/callback?code=foo&state=state-wrong",
		port))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "会话状态不匹配") {
		t.Errorf("body did not contain state_mismatch page: %s", body)
	}
	// Channel should ONLY close on timeout, no result.
	select {
	case res, ok := <-ch:
		if ok {
			t.Errorf("unexpected result on state mismatch: %+v", res)
		}
		// channel was closed by timeout — acceptable
	case <-time.After(time.Second):
		t.Fatal("channel neither closed nor received (timeout should fire)")
	}
}

func TestStartListening_MissingCode(t *testing.T) {
	cfg := AuthCodeConfig{
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{freePort(t)},
		LoginTimeout: 500 * time.Millisecond,
	}
	port, ln, err := ReservePort(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, shutdown := StartListening(ctx, ln, cfg, "state-x")
	defer shutdown()

	// state matches but no code / no error → handler should serve missing_code page
	// and NOT send.
	resp, _ := http.Get(fmt.Sprintf(
		"http://127.0.0.1:%d/oauth/modelserver/callback?state=state-x",
		port))
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "回调缺少授权码") {
		t.Errorf("body did not contain missing_code page: %s", body)
	}
	select {
	case res, ok := <-ch:
		if ok {
			t.Errorf("unexpected result: %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not close on timeout")
	}
}

func TestStartListening_CtxCancel(t *testing.T) {
	cfg := AuthCodeConfig{
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{freePort(t)},
		LoginTimeout: 30 * time.Second, // long; we cancel manually
	}
	_, ln, err := ReservePort(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, shutdown := StartListening(ctx, ln, cfg, "state-x")

	cancel()
	// shutdown is idempotent — calling after cancel must not panic
	shutdown()
	shutdown()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed, got value")
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not close after ctx cancel")
	}
}
