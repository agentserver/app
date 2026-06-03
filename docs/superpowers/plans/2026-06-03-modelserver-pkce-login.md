# Modelserver PKCE Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Switch the modelserver login path from device-code (which would need ops to enable a server-side wrapper) to authorization_code + PKCE on the public client ops already registered (`5321f7e6-3d79-4ac9-a742-04809dbf9025`). The agentserver login path stays on device code unchanged.

**Architecture:** Add a new PKCE flow to `internal/oauth/` (protocol layer + one-shot loopback HTTP listener), keep `devicecode.go` untouched. Rewrite `realOrchestrator.LoginModelserver` / `PollModelserverLogin` to use the PKCE flow while preserving the two-step HTTP API shape (so the agentserver path's `LoginAgentserver`/`PollAgentserverLogin` symmetry holds). Front-end drops the `user_code` alert in the modelserver branch only. Fake integration server grows `/oauth2/auth` + `authorization_code` `/oauth2/token` handlers.

**Tech Stack:** Go 1.26 (stdlib `net/http`, `crypto/sha256`, `crypto/rand`, `encoding/base64`, `html/template`, `embed`), no third-party OAuth library.

**Spec:** `docs/superpowers/specs/2026-06-03-modelserver-pkce-login-design.md`

---

## File Structure

**Created (new files):**
- `internal/oauth/authcode_pkce.go` — Pure PKCE protocol layer: `AuthCodeConfig`, `PKCESession`, `StartPKCE`, `FinishPKCE`. No goroutines, no listeners.
- `internal/oauth/pkce_callback.go` — Loopback HTTP listener. `ReservePort`, `StartListening`, `CallbackResult`, `ErrAllPortsBusy`. Owns embedded HTML pages.
- `internal/oauth/templates/success.html` — "登录成功，可关闭此页"
- `internal/oauth/templates/denied.html` — "登录被拒绝"
- `internal/oauth/templates/state_mismatch.html` — "会话状态不匹配"
- `internal/oauth/templates/missing_code.html` — "回调缺少授权码"
- `internal/oauth/authcode_pkce_test.go` — Unit tests for the protocol layer.
- `internal/oauth/pkce_callback_test.go` — Unit tests for the listener layer.

**Modified:**
- `internal/oauth/types.go` — Add `AuthCodeConfig` is in `authcode_pkce.go`, types.go untouched. (Keeping `Config` for device code, no shared edits.)
- `internal/ui/orchestrator.go:18,62-67` — Change `LoginModelserver` signature; update noop.
- `internal/ui/orchestrator_real.go:33-63,87-125` — Add `OpenBrowser` to `Deps`, replace `msChallenge` with PKCE session/channel/shutdown, rewrite both methods.
- `internal/ui/server.go:64-74` — Strip `openBrowser` + return-challenge from `handleMSLogin`.
- `internal/ui/assets/app.js:55-63` — Split modelserver / agentserver branches; drop user_code alert in MS branch.
- `internal/ui/server_test.go:23-41` — Update modelserver step assertion (no `user_code`).
- `internal/ui/orchestrator_real_test.go` — Add 2 PKCE tests (no existing MS login tests to remove).
- `cmd/launcher/main.go:60-70,89-104` — Switch `msOAuth` type + add `OpenBrowser` to Deps + drop `openBrowser` from `NewServer` invocation for MS (still used for AS initial).
- `test/integration/fakeserver/fakeserver.go:35-85` — Add `/oauth2/auth` (302 to callback) and extend `/api/oauth2/token` to accept `grant_type=authorization_code`.
- `test/integration/flows/full_onboarding_test.go:37-39` — Switch `MSOAuth` type to `AuthCodeConfig`.
- `test/integration/flows/resume_test.go:32-34` — Same.
- `test/integration/flows/idempotent_test.go:31-33` — Same.
- `docs/ops/modelserver-oauth-client-registration.md` — Rewrite as v6 "已完成 + 链路文档".

---

## Task 1: Add `AuthCodeConfig` + `PKCESession` skeleton with first test

**Files:**
- Create: `internal/oauth/authcode_pkce.go`
- Create: `internal/oauth/authcode_pkce_test.go`

- [ ] **Step 1: Write the failing test for `StartPKCE` S256 output shape**

```go
// internal/oauth/authcode_pkce_test.go
package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStartPKCE_S256(t *testing.T) {
	cfg := AuthCodeConfig{
		Endpoint:  "https://codeapi.cs.ac.cn",
		AuthPath:  "/oauth2/auth",
		TokenPath: "/oauth2/token",
		ClientID:  "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:     "project:inference offline_access",
	}
	sess, err := StartPKCE(cfg, "http://127.0.0.1:53428/oauth/modelserver/callback")
	if err != nil {
		t.Fatalf("StartPKCE: %v", err)
	}
	if l := len(sess.Verifier); l < 43 || l > 128 {
		t.Errorf("Verifier length %d not in [43,128]", l)
	}
	sum := sha256.Sum256([]byte(sess.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if sess.Challenge != want {
		t.Errorf("Challenge = %q, want %q", sess.Challenge, want)
	}
	if len(sess.State) < 16 {
		t.Errorf("State too short: %d", len(sess.State))
	}
	if sess.RedirectURI != "http://127.0.0.1:53428/oauth/modelserver/callback" {
		t.Errorf("RedirectURI = %q", sess.RedirectURI)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestStartPKCE_S256 -v`
Expected: FAIL with "undefined: AuthCodeConfig" or "undefined: StartPKCE".

- [ ] **Step 3: Write minimal `authcode_pkce.go` to make the test pass**

```go
// internal/oauth/authcode_pkce.go
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"time"
)

// AuthCodeConfig is OAuth 2.0 authorization_code with PKCE (RFC 7636).
// Used by the modelserver login path. Separate from Config (device code)
// because the two flows share only the Token type — mixing them in one
// struct invites silent misuse.
type AuthCodeConfig struct {
	Endpoint     string        // "https://codeapi.cs.ac.cn"
	AuthPath     string        // "/oauth2/auth"
	TokenPath    string        // "/oauth2/token"
	ClientID     string        // "5321f7e6-3d79-4ac9-a742-04809dbf9025"
	Scope        string        // "project:inference offline_access"
	CallbackPath string        // "/oauth/modelserver/callback"
	Ports        []int         // [53428..53435]
	LoginTimeout time.Duration // upper bound on a single login (default 10m, set by StartListening)
}

func (c AuthCodeConfig) AuthURL() string  { return joinURL(c.Endpoint, c.AuthPath) }
func (c AuthCodeConfig) TokenURL() string { return joinURL(c.Endpoint, c.TokenPath) }

// PKCESession is one in-flight login attempt.
// Constructed by StartPKCE with a known redirectURI; consumed by FinishPKCE.
// Never reuse.
type PKCESession struct {
	Verifier    string // 43-128 chars base64url (RFC 7636 §4.1)
	Challenge   string // base64url(sha256(verifier))
	State       string // CSRF nonce, ≥16 bytes base64url
	RedirectURI string // full http://127.0.0.1:<port>/<callbackPath>
	AuthURL     string // pre-built browser URL
}

// StartPKCE generates verifier/challenge/state and pre-builds the auth URL.
// The caller MUST have already reserved a callback port (via ReservePort)
// and passed the resulting redirectURI here; AuthURL embeds it.
func StartPKCE(cfg AuthCodeConfig, redirectURI string) (*PKCESession, error) {
	verifier, err := randomURLSafe(64)
	if err != nil {
		return nil, fmt.Errorf("pkce verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	state, err := randomURLSafe(16)
	if err != nil {
		return nil, fmt.Errorf("pkce state: %w", err)
	}
	sess := &PKCESession{
		Verifier:    verifier,
		Challenge:   challenge,
		State:       state,
		RedirectURI: redirectURI,
	}
	sess.AuthURL = buildAuthURL(cfg, sess)
	return sess, nil
}

// buildAuthURL composes the OAuth /oauth2/auth URL with all PKCE params.
func buildAuthURL(cfg AuthCodeConfig, sess *PKCESession) string {
	// Defined fully in Task 2; stubbed here so the first test compiles.
	return cfg.AuthURL()
}

// randomURLSafe returns n bytes of crypto/rand encoded as base64url-without-padding.
func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
```

- [ ] **Step 4: Run test and verify it passes**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestStartPKCE_S256 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/authcode_pkce.go internal/oauth/authcode_pkce_test.go
git commit -m "feat(oauth): add AuthCodeConfig + StartPKCE with S256 challenge"
```

---

## Task 2: Flesh out `buildAuthURL` with full PKCE query string

**Files:**
- Modify: `internal/oauth/authcode_pkce.go` (replace stubbed `buildAuthURL`)
- Modify: `internal/oauth/authcode_pkce_test.go` (add test)

- [ ] **Step 1: Add the failing test**

```go
// Append to internal/oauth/authcode_pkce_test.go
func TestStartPKCE_AuthURL(t *testing.T) {
	cfg := AuthCodeConfig{
		Endpoint: "https://codeapi.cs.ac.cn",
		AuthPath: "/oauth2/auth",
		ClientID: "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:    "project:inference offline_access",
	}
	sess, err := StartPKCE(cfg, "http://127.0.0.1:53428/oauth/modelserver/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(sess.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "codeapi.cs.ac.cn" || u.Path != "/oauth2/auth" {
		t.Errorf("AuthURL base wrong: %q", sess.AuthURL)
	}
	q := u.Query()
	for _, k := range []string{"response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method"} {
		if q.Get(k) == "" {
			t.Errorf("AuthURL missing %s", k)
		}
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") != sess.Challenge {
		t.Errorf("code_challenge != session.Challenge")
	}
	if q.Get("state") != sess.State {
		t.Errorf("state != session.State")
	}
	if q.Get("redirect_uri") != sess.RedirectURI {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), sess.RedirectURI)
	}
	if !strings.Contains(q.Get("scope"), "project:inference") {
		t.Errorf("scope = %q", q.Get("scope"))
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestStartPKCE_AuthURL -v`
Expected: FAIL — `AuthURL missing response_type` etc. (stub returns just `cfg.AuthURL()`).

- [ ] **Step 3: Replace the stubbed `buildAuthURL`**

```go
// Replace the stubbed buildAuthURL in internal/oauth/authcode_pkce.go:
import (
	// ... existing imports ...
	"net/url"
)

// buildAuthURL composes the OAuth /oauth2/auth URL with all PKCE params.
func buildAuthURL(cfg AuthCodeConfig, sess *PKCESession) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", sess.RedirectURI)
	q.Set("scope", cfg.Scope)
	q.Set("state", sess.State)
	q.Set("code_challenge", sess.Challenge)
	q.Set("code_challenge_method", "S256")
	return cfg.AuthURL() + "?" + q.Encode()
}
```

- [ ] **Step 4: Run both PKCE tests to verify**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestStartPKCE -v`
Expected: PASS (both `TestStartPKCE_S256` and `TestStartPKCE_AuthURL`).

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/authcode_pkce.go internal/oauth/authcode_pkce_test.go
git commit -m "feat(oauth): build PKCE auth URL with full query params"
```

---

## Task 3: `FinishPKCE` token exchange (success path)

**Files:**
- Modify: `internal/oauth/authcode_pkce.go` (add `FinishPKCE`)
- Modify: `internal/oauth/authcode_pkce_test.go` (add test)

- [ ] **Step 1: Write the failing test**

```go
// Append to internal/oauth/authcode_pkce_test.go
import (
	// ... existing ...
	"context"
	"net/http"
	"net/http/httptest"
)

func TestFinishPKCE_Success(t *testing.T) {
	var gotBody url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		gotBody = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"tok-xyz","token_type":"Bearer","refresh_token":"rtok","expires_in":3600}`))
	}))
	defer srv.Close()

	cfg := AuthCodeConfig{
		Endpoint:  srv.URL,
		TokenPath: "/oauth2/token",
		ClientID:  "client-x",
	}
	sess := &PKCESession{
		Verifier:    "verifier-xyz",
		RedirectURI: "http://127.0.0.1:53428/oauth/modelserver/callback",
	}
	tok, err := FinishPKCE(context.Background(), cfg, sess, "code-abc")
	if err != nil {
		t.Fatalf("FinishPKCE: %v", err)
	}
	if tok.AccessToken != "tok-xyz" || tok.RefreshToken != "rtok" || tok.TokenType != "Bearer" {
		t.Errorf("token = %+v", tok)
	}
	if gotBody.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", gotBody.Get("grant_type"))
	}
	if gotBody.Get("code") != "code-abc" {
		t.Errorf("code = %q", gotBody.Get("code"))
	}
	if gotBody.Get("code_verifier") != "verifier-xyz" {
		t.Errorf("code_verifier = %q", gotBody.Get("code_verifier"))
	}
	if gotBody.Get("client_id") != "client-x" {
		t.Errorf("client_id = %q", gotBody.Get("client_id"))
	}
	if gotBody.Get("redirect_uri") != "http://127.0.0.1:53428/oauth/modelserver/callback" {
		t.Errorf("redirect_uri = %q", gotBody.Get("redirect_uri"))
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestFinishPKCE_Success -v`
Expected: FAIL with `undefined: FinishPKCE`.

- [ ] **Step 3: Implement `FinishPKCE`**

```go
// Append to internal/oauth/authcode_pkce.go:
import (
	// add: context, encoding/json, net/http, strings
)

// FinishPKCE exchanges the auth code for tokens using the PKCE verifier.
// Public client: no client_secret, verifier proves the caller is the same
// process that initiated the flow.
func FinishPKCE(ctx context.Context, cfg AuthCodeConfig, sess *PKCESession, code string) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("code_verifier", sess.Verifier)
	form.Set("client_id", cfg.ClientID)
	form.Set("redirect_uri", sess.RedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL(),
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var te tokenErr
		if err := json.NewDecoder(resp.Body).Decode(&te); err == nil && te.Code != "" {
			return Token{}, fmt.Errorf("token exchange: %s: %s", te.Code, te.Desc)
		}
		return Token{}, fmt.Errorf("token exchange: status %d", resp.StatusCode)
	}
	var tok Token
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return Token{}, fmt.Errorf("decode token: %w", err)
	}
	return tok, nil
}
```

(Note `tokenErr` already exists in `devicecode.go`; same package, no import needed.)

- [ ] **Step 4: Run test to verify pass**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestFinishPKCE_Success -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/authcode_pkce.go internal/oauth/authcode_pkce_test.go
git commit -m "feat(oauth): add FinishPKCE token exchange"
```

---

## Task 4: `FinishPKCE` invalid_grant error path

**Files:**
- Modify: `internal/oauth/authcode_pkce_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Append to internal/oauth/authcode_pkce_test.go
func TestFinishPKCE_InvalidGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"code used or expired"}`))
	}))
	defer srv.Close()

	cfg := AuthCodeConfig{Endpoint: srv.URL, TokenPath: "/oauth2/token", ClientID: "x"}
	sess := &PKCESession{Verifier: "v", RedirectURI: "http://127.0.0.1:1/cb"}

	_, err := FinishPKCE(context.Background(), cfg, sess, "stale-code")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want to contain 'invalid_grant'", err.Error())
	}
}
```

- [ ] **Step 2: Run — should already pass thanks to Task 3's error handling**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestFinishPKCE_InvalidGrant -v`
Expected: PASS (Task 3's `FinishPKCE` already wraps `te.Code` into the error).

If FAIL: the test caught an actual error-handling bug — fix `FinishPKCE` to include `te.Code` in its error message and re-run.

- [ ] **Step 3: Run the full oauth test suite to confirm no regression**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -v`
Expected: All tests PASS.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/authcode_pkce_test.go
git commit -m "test(oauth): cover invalid_grant from token endpoint"
```

---

## Task 5: `ReservePort` — pick first listenable port from list

**Files:**
- Create: `internal/oauth/pkce_callback.go`
- Create: `internal/oauth/pkce_callback_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/oauth/pkce_callback_test.go
package oauth

import (
	"errors"
	"fmt"
	"net"
	"testing"
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

// Silence unused for now (used in later tasks).
var _ = fmt.Sprintf
var _ = freePort
```

- [ ] **Step 2: Run to confirm it fails**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestReservePort -v`
Expected: FAIL with `undefined: ReservePort` / `undefined: ErrAllPortsBusy`.

- [ ] **Step 3: Implement `ReservePort` + `ErrAllPortsBusy`**

```go
// internal/oauth/pkce_callback.go
package oauth

import (
	"errors"
	"fmt"
	"net"
)

// ErrAllPortsBusy is returned by ReservePort when none of cfg.Ports could be bound.
var ErrAllPortsBusy = errors.New("all configured callback ports are busy")

// ReservePort tries cfg.Ports in order, returns the first that net.Listen accepts.
// The returned listener is held for StartListening; caller MUST hand it off or .Close() it.
func ReservePort(cfg AuthCodeConfig) (port int, ln net.Listener, err error) {
	for _, p := range cfg.Ports {
		l, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if lerr == nil {
			return p, l, nil
		}
	}
	return 0, nil, ErrAllPortsBusy
}
```

- [ ] **Step 4: Run and verify pass**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestReservePort -v`
Expected: PASS for both subtests.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/pkce_callback.go internal/oauth/pkce_callback_test.go
git commit -m "feat(oauth): ReservePort tries listenable ports in order"
```

---

## Task 6: Embed four Chinese callback HTML pages

**Files:**
- Create: `internal/oauth/templates/success.html`
- Create: `internal/oauth/templates/denied.html`
- Create: `internal/oauth/templates/state_mismatch.html`
- Create: `internal/oauth/templates/missing_code.html`
- Modify: `internal/oauth/pkce_callback.go` (add embed + accessor)
- Modify: `internal/oauth/pkce_callback_test.go` (add a sanity test)

- [ ] **Step 1: Create the four HTML files**

```html
<!-- internal/oauth/templates/success.html -->
<!doctype html>
<html lang="zh-cn"><head><meta charset="utf-8"><title>登录成功</title>
<style>body{font-family:system-ui,sans-serif;max-width:480px;margin:80px auto;padding:0 24px;color:#222}h1{font-size:22px;color:#0a7d28}p{color:#555;line-height:1.6}</style>
</head><body><h1>✓ 登录成功</h1><p>可关闭此页, 回到安装窗口继续。</p></body></html>
```

```html
<!-- internal/oauth/templates/denied.html -->
<!doctype html>
<html lang="zh-cn"><head><meta charset="utf-8"><title>登录被拒绝</title>
<style>body{font-family:system-ui,sans-serif;max-width:480px;margin:80px auto;padding:0 24px;color:#222}h1{font-size:22px;color:#b00020}p{color:#555;line-height:1.6}</style>
</head><body><h1>✗ 登录被拒绝</h1><p>如需重试, 请回到安装窗口点 "重试" 按钮。</p></body></html>
```

```html
<!-- internal/oauth/templates/state_mismatch.html -->
<!doctype html>
<html lang="zh-cn"><head><meta charset="utf-8"><title>会话状态不匹配</title>
<style>body{font-family:system-ui,sans-serif;max-width:480px;margin:80px auto;padding:0 24px;color:#222}h1{font-size:22px;color:#b00020}p{color:#555;line-height:1.6}</style>
</head><body><h1>✗ 会话状态不匹配</h1><p>本浏览器页面可能已过期, 或是被其他来源跳转访问。请回到安装窗口重试。</p></body></html>
```

```html
<!-- internal/oauth/templates/missing_code.html -->
<!doctype html>
<html lang="zh-cn"><head><meta charset="utf-8"><title>回调缺少授权码</title>
<style>body{font-family:system-ui,sans-serif;max-width:480px;margin:80px auto;padding:0 24px;color:#222}h1{font-size:22px;color:#b00020}p{color:#555;line-height:1.6}</style>
</head><body><h1>✗ 回调缺少授权码</h1><p>请回到安装窗口重试。</p></body></html>
```

- [ ] **Step 2: Add embed + test asserting templates load**

Append to `internal/oauth/pkce_callback.go`:

```go
import (
	// add: embed
)

//go:embed templates/success.html templates/denied.html templates/state_mismatch.html templates/missing_code.html
var callbackTemplates embed.FS

// callbackPage returns the embedded HTML for the named outcome.
// Panics on unknown name (programmer error).
func callbackPage(name string) []byte {
	b, err := callbackTemplates.ReadFile("templates/" + name + ".html")
	if err != nil {
		panic("oauth: missing embedded template " + name + ": " + err.Error())
	}
	return b
}
```

Append to `internal/oauth/pkce_callback_test.go`:

```go
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
```

(Add `"strings"` to test imports.)

- [ ] **Step 3: Run and verify**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestCallbackPages -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/templates/ internal/oauth/pkce_callback.go internal/oauth/pkce_callback_test.go
git commit -m "feat(oauth): embed four Chinese callback HTML pages"
```

---

## Task 7: `StartListening` success path (code + matching state)

**Files:**
- Modify: `internal/oauth/pkce_callback.go` (add `CallbackResult`, `StartListening`)
- Modify: `internal/oauth/pkce_callback_test.go` (add test)

- [ ] **Step 1: Write the failing test**

```go
// Append to internal/oauth/pkce_callback_test.go
import (
	// add: context, io, net/http, time
)

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
```

- [ ] **Step 2: Run to confirm it fails**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestStartListening_Success -v`
Expected: FAIL with `undefined: CallbackResult` / `undefined: StartListening`.

- [ ] **Step 3: Implement `CallbackResult` + `StartListening`**

Append to `internal/oauth/pkce_callback.go`:

```go
import (
	// add: context, net/http, sync, time
)

// CallbackResult is what arrives at the redirect_uri.
// Sent on the channel returned by StartListening.
type CallbackResult struct {
	Code  string
	State string
	Error string // OAuth error code if present (e.g. "access_denied")
}

// StartListening serves cfg.CallbackPath on ln. The handler:
//   - On valid code+state match → send {Code, State} on channel, serve success.html
//   - On error= → send {Error} on channel, serve denied.html
//   - On state mismatch → DO NOT send, serve state_mismatch.html (caller times out)
//   - On missing code & no error → DO NOT send, serve missing_code.html
// The channel receives at most one value; closed by shutdown().
// Caller MUST call shutdown(); idempotent. ctx with LoginTimeout (default 10m)
// closes the channel on expiry to bound waits.
func StartListening(ctx context.Context, ln net.Listener, cfg AuthCodeConfig, expectedState string) (
	ch <-chan CallbackResult, shutdown func(),
) {
	timeout := cfg.LoginTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)

	out := make(chan CallbackResult, 1)
	srv := &http.Server{}

	var once sync.Once
	sendOnce := func(r CallbackResult) {
		select {
		case out <- r:
		default:
		}
	}
	closeOnce := func() { once.Do(func() { close(out) }) }

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(callbackPage("denied"))
			sendOnce(CallbackResult{Error: e})
			return
		}
		state := q.Get("state")
		if state != expectedState {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(callbackPage("state_mismatch"))
			return // no send → caller will time out
		}
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(callbackPage("missing_code"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(callbackPage("success"))
		sendOnce(CallbackResult{Code: code, State: state})
	})
	srv.Handler = mux

	go func() { _ = srv.Serve(ln) }()

	// Watcher: close channel + shut server when ctx expires.
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
		closeOnce()
	}()

	shutdown = func() {
		cancel()
		_ = srv.Shutdown(context.Background())
		closeOnce()
	}
	return out, shutdown
}
```

- [ ] **Step 4: Run and verify**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestStartListening_Success -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/pkce_callback.go internal/oauth/pkce_callback_test.go
git commit -m "feat(oauth): StartListening serves callback and surfaces code"
```

---

## Task 8: `StartListening` error paths (denied, state mismatch, missing code, ctx cancel)

**Files:**
- Modify: `internal/oauth/pkce_callback_test.go` (add 4 tests)

- [ ] **Step 1: Write the four failing tests**

```go
// Append to internal/oauth/pkce_callback_test.go

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
```

- [ ] **Step 2: Run all four**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -run TestStartListening -v -race`
Expected: All four PASS (the implementation from Task 7 already handles these branches).

If any FAIL: fix the corresponding branch in `StartListening` (do not weaken the test). Common likely fix: ensure `closeOnce` runs in `shutdown` even when the timeout watcher hasn't fired.

- [ ] **Step 3: Run the full oauth suite**

Run: `cd /root/agentserver-pkg && go test ./internal/oauth/ -v -race`
Expected: All PASS — `TestStartPKCE_S256`, `TestStartPKCE_AuthURL`, `TestFinishPKCE_Success`, `TestFinishPKCE_InvalidGrant`, `TestReservePort_FirstFree`, `TestReservePort_AllBusy`, `TestCallbackPages_AllPresent`, `TestStartListening_Success`, `TestStartListening_OAuthError`, `TestStartListening_StateMismatch`, `TestStartListening_MissingCode`, `TestStartListening_CtxCancel`. Existing `TestDeviceCode*` etc. all still PASS.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver-pkg
git add internal/oauth/pkce_callback_test.go
git commit -m "test(oauth): cover StartListening error paths + ctx cancel"
```

---

## Task 9: Wire `AuthCodeConfig` into `Deps` and `Orchestrator` interface

**Files:**
- Modify: `internal/ui/orchestrator.go`
- Modify: `internal/ui/orchestrator_real.go` (Deps + receiver fields only; methods come next task)

- [ ] **Step 1: Change `Orchestrator` interface and update noopOrchestrator**

Edit `internal/ui/orchestrator.go`:

```go
// Replace lines 18-19 and 62-67. The full updated interface and noop:

type Orchestrator interface {
	State(ctx context.Context) (SanitizedState, error)

	LoginModelserver(ctx context.Context) error
	PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error)

	LoginAgentserver(ctx context.Context) (oauth.DeviceCodeChallenge, error)
	PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error)

	EnsureVSCode(ctx context.Context, progress chan<- ProgressEvent) error
	ConfigureVSCode(ctx context.Context) error

	Finalize(ctx context.Context) error
	Abort(ctx context.Context) error
}
```

And:

```go
// Replace the two noopOrchestrator login methods:
func (noopOrchestrator) LoginModelserver(context.Context) error { return nil }
func (noopOrchestrator) PollModelserverLogin(context.Context) (modelserver.APIKey, error) {
	return modelserver.APIKey{}, nil
}
```

Leave `LoginAgentserver` / `PollAgentserverLogin` and their noop implementations untouched.

- [ ] **Step 2: Update `Deps` and `realOrchestrator` fields**

Edit `internal/ui/orchestrator_real.go`. Replace lines 33-63:

```go
type Deps struct {
	State             *state.Store
	Secrets           secrets.Store
	MS                *modelserver.Client
	AS                *agentserver.Client
	MSOAuth           oauth.AuthCodeConfig // PKCE (modelserver path)
	ASOAuth           oauth.Config         // device code (agentserver path)
	CodexConfigPath   string
	VSCodeUserDataDir string
	VSCodeExtDir      string
	EmbeddedVSIXPath  string
	CodexAbsPath      string
	// CodexDownloadURL overrides the default GitHub Releases URL when set.
	CodexDownloadURL string

	// OpenBrowser is invoked by the orchestrator after starting the PKCE
	// listener. Optional in tests.
	OpenBrowser func(string)

	// Used by Finalize (set by launcher; see P9.3)
	LauncherExePath   string
	OpenFolderExePath string
	IconPath          string
}

type realOrchestrator struct {
	d Deps
	// modelserver PKCE in-flight session:
	msSession  *oauth.PKCESession
	msCallback <-chan oauth.CallbackResult
	msShutdown func()
	// agentserver device-code in-flight challenge (unchanged):
	asChallenge oauth.DeviceCodeChallenge
	msToken     oauth.Token
	asToken     oauth.Token
}
```

- [ ] **Step 3: Confirm the package fails to build (LoginModelserver impl now mismatches new signature)**

Run: `cd /root/agentserver-pkg && go build ./internal/ui/`
Expected: BUILD FAILS — `*realOrchestrator does not implement Orchestrator (wrong type for method LoginModelserver)` and `unknown field msChallenge`. This is expected; Task 10 replaces the methods.

- [ ] **Step 4: No commit yet — Task 10 finishes the wiring**

(Skipped: the working tree is mid-refactor.)

---

## Task 10: Implement new `LoginModelserver` and `PollModelserverLogin`

**Files:**
- Modify: `internal/ui/orchestrator_real.go` (replace lines 87-125)

- [ ] **Step 1: Replace the two methods**

Edit `internal/ui/orchestrator_real.go`. Replace `LoginModelserver` and `PollModelserverLogin` (and keep `LoginAgentserver` / `PollAgentserverLogin` untouched below them):

```go
import (
	// add: errors, fmt
	// (fmt likely already imported)
)

func (r *realOrchestrator) LoginModelserver(ctx context.Context) error {
	port, ln, err := oauth.ReservePort(r.d.MSOAuth)
	if err != nil {
		if errors.Is(err, oauth.ErrAllPortsBusy) {
			return fmt.Errorf("OAuth 回调端口 %v 全部被占用, 请关闭其他 agentserver-vscode 进程后重试",
				r.d.MSOAuth.Ports)
		}
		return err
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, r.d.MSOAuth.CallbackPath)
	sess, err := oauth.StartPKCE(r.d.MSOAuth, redirectURI)
	if err != nil {
		_ = ln.Close()
		return err
	}
	ch, shutdown := oauth.StartListening(ctx, ln, r.d.MSOAuth, sess.State)
	r.msSession = sess
	r.msCallback = ch
	r.msShutdown = shutdown

	if r.d.OpenBrowser != nil {
		go r.d.OpenBrowser(sess.AuthURL)
	}
	return nil
}

func (r *realOrchestrator) PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error) {
	if r.msSession == nil {
		return modelserver.APIKey{}, fmt.Errorf("no in-flight modelserver login")
	}
	select {
	case res, ok := <-r.msCallback:
		if !ok {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("登录会话已结束, 请重试")
		}
		if res.Error != "" {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("登录被拒绝: %s", res.Error)
		}
		if res.State != r.msSession.State {
			r.cleanupMS()
			return modelserver.APIKey{}, fmt.Errorf("会话状态不匹配, 请重试")
		}
		tok, err := oauth.FinishPKCE(ctx, r.d.MSOAuth, r.msSession, res.Code)
		if err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		r.msToken = tok
		proj, err := r.d.MS.PickOrCreateProject(ctx, tok.AccessToken, "default")
		if err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		key, err := r.d.MS.CreateAPIKey(ctx, tok.AccessToken, proj.ID, "agentserver-vscode")
		if err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		if err := r.d.Secrets.Set("modelserver_api_key", key.Secret); err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		if err := r.d.State.Update(func(s *state.State) error {
			s.Modelserver.ProjectID = proj.ID
			s.Modelserver.APIKeySuffix = key.KeySuffix
			s.Onboarding.AddCompleted("modelserver_login")
			return nil
		}); err != nil {
			r.cleanupMS()
			return modelserver.APIKey{}, err
		}
		r.cleanupMS()
		return key, nil
	case <-ctx.Done():
		// Do NOT cleanup: caller (server.handleMSStatus) wraps Poll in a 30s
		// timeout and re-polls. Session stays armed until callback arrives or
		// the PKCE listener's own 10-minute timeout fires.
		return modelserver.APIKey{}, ctx.Err()
	}
}

func (r *realOrchestrator) cleanupMS() {
	if r.msShutdown != nil {
		r.msShutdown()
	}
	r.msSession = nil
	r.msCallback = nil
	r.msShutdown = nil
}
```

- [ ] **Step 2: Run the build**

Run: `cd /root/agentserver-pkg && go build ./internal/ui/`
Expected: BUILD PASSES.

- [ ] **Step 3: Run all UI tests to find what we broke**

Run: `cd /root/agentserver-pkg && go test ./internal/ui/ -v`
Expected: Some FAILURES in `internal/ui/server_test.go` (the old `TestServerStepEndpoint` asserts on `user_code`) and possibly nothing else (no existing tests touch `LoginModelserver/PollModelserverLogin` directly besides via noop).

Note which tests fail; the next two tasks fix them.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver-pkg
git add internal/ui/orchestrator.go internal/ui/orchestrator_real.go
git commit -m "feat(ui): rewrite LoginModelserver/PollModelserverLogin for PKCE"
```

---

## Task 11: Strip browser-open + return-challenge from `server.handleMSLogin`

**Files:**
- Modify: `internal/ui/server.go` (lines 64-74)
- Modify: `internal/ui/server_test.go` (update `TestServerStepEndpoint`)

- [ ] **Step 1: Update `handleMSLogin`**

Replace lines 64-74 in `internal/ui/server.go`:

```go
func (s *server) handleMSLogin(w http.ResponseWriter, r *http.Request) {
	if err := s.o.LoginModelserver(r.Context()); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "started"})
}
```

`handleMSStatus`, `handleASLogin`, `handleASStatus` unchanged. `NewServer`
signature unchanged — `openBrowser` is still used for the initial onboarding
URL (in main.go) and for agentserver's verification URI (in `handleASLogin`).

- [ ] **Step 2: Update `TestServerStepEndpoint`**

Replace `TestServerStepEndpoint` in `internal/ui/server_test.go`:

```go
func TestServerStepEndpoint(t *testing.T) {
	srv := httptest.NewServer(NewServer(noopOrchestrator{}, nil))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/step/modelserver_login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["state"] != "started" {
		t.Errorf("got %+v, want state=started", body)
	}
}
```

- [ ] **Step 3: Run UI tests**

Run: `cd /root/agentserver-pkg && go test ./internal/ui/ -v`
Expected: All PASS. (The agentserver path is untouched and still works against `noopOrchestrator`'s `LoginAgentserver` returning `{UserCode:"TEST"}`.)

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver-pkg
git add internal/ui/server.go internal/ui/server_test.go
git commit -m "feat(ui): handleMSLogin returns {state:started}, no challenge"
```

---

## Task 12: Add 2 orchestrator-level PKCE tests

**Files:**
- Modify: `internal/ui/orchestrator_real_test.go`

- [ ] **Step 1: Write the LoginModelserver test**

Append to `internal/ui/orchestrator_real_test.go`:

```go
import (
	// add (if not present):
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
)

// freeUIPort returns a port that's free *at the moment of call*. Race-prone
// in theory; fine in practice because the orchestrator binds it immediately.
func freeUIPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestLoginModelserver_StartsListenerOpensBrowser(t *testing.T) {
	port := freeUIPort(t)

	var openedURL string
	var openedOnce sync.WaitGroup
	openedOnce.Add(1)
	openBrowser := func(u string) {
		openedURL = u
		openedOnce.Done()
	}

	cfg := oauth.AuthCodeConfig{
		Endpoint:     "https://hydra.example",
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{port},
		LoginTimeout: 2 * time.Second,
	}

	dir := t.TempDir()
	r := &realOrchestrator{d: Deps{
		State:       state.NewStore(filepath.Join(dir, "state.json")),
		Secrets:     secrets.New(filepath.Join(dir, "secrets.json")),
		MSOAuth:     cfg,
		OpenBrowser: openBrowser,
	}}

	if err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatalf("LoginModelserver: %v", err)
	}
	defer r.cleanupMS()

	// Browser should have been invoked async with the auth URL.
	done := make(chan struct{})
	go func() { openedOnce.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OpenBrowser was not called")
	}
	u, _ := url.Parse(openedURL)
	q := u.Query()
	if q.Get("client_id") != "5321f7e6-3d79-4ac9-a742-04809dbf9025" {
		t.Errorf("client_id missing or wrong: %q", openedURL)
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if !strings.HasPrefix(q.Get("redirect_uri"), fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback", port)) {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}
```

(Add `"fmt"` and `"path/filepath"` to imports if not present.)

- [ ] **Step 2: Run it**

Run: `cd /root/agentserver-pkg && go test ./internal/ui/ -run TestLoginModelserver_StartsListenerOpensBrowser -v`
Expected: PASS.

- [ ] **Step 3: Write the full-flow PKCE test**

Append to `internal/ui/orchestrator_real_test.go`:

```go
func TestPollModelserverLogin_FullPKCE(t *testing.T) {
	port := freeUIPort(t)

	// Fake modelserver: serves both Hydra /oauth2/token and the
	// admin /api/v1/projects + /api/v1/projects/{id}/keys.
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != "code-abc" ||
			r.PostForm.Get("code_verifier") == "" {
			t.Errorf("/oauth2/token bad form: %v", r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"fake-at","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(201)
			w.Write([]byte(`{"data":{"id":"proj-1","name":"default"}}`))
			return
		}
		w.Write([]byte(`{"data":[]}`))
	})
	mux.HandleFunc("/api/v1/projects/proj-1/keys", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte(`{"data":{"id":"k1","key_suffix":"wxyz"},"key":"ms-fakekey-xxx"}`))
	})
	fake := httptest.NewServer(mux)
	defer fake.Close()

	openBrowser := func(string) {} // no-op; we issue the callback directly

	cfg := oauth.AuthCodeConfig{
		Endpoint:     fake.URL,
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "client-x",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{port},
		LoginTimeout: 3 * time.Second,
	}

	dir := t.TempDir()
	sec := secrets.New(filepath.Join(dir, "secrets.json"))
	store := state.NewStore(filepath.Join(dir, "state.json"))

	r := &realOrchestrator{d: Deps{
		State:       store,
		Secrets:     sec,
		MS:          modelserver.New(fake.URL),
		MSOAuth:     cfg,
		OpenBrowser: openBrowser,
	}}

	if err := r.LoginModelserver(context.Background()); err != nil {
		t.Fatalf("LoginModelserver: %v", err)
	}

	// Simulate the browser hitting the callback.
	go func() {
		// Tiny delay so PollModelserverLogin gets to the select first
		// (not strictly required since the channel is buffered).
		time.Sleep(50 * time.Millisecond)
		callbackURL := fmt.Sprintf("http://127.0.0.1:%d/oauth/modelserver/callback?code=code-abc&state=%s",
			port, r.msSession.State)
		_, _ = http.Get(callbackURL)
	}()

	key, err := r.PollModelserverLogin(context.Background())
	if err != nil {
		t.Fatalf("PollModelserverLogin: %v", err)
	}
	if key.Secret != "ms-fakekey-xxx" {
		t.Errorf("key.Secret = %q", key.Secret)
	}
	if got, _ := sec.Get("modelserver_api_key"); got != "ms-fakekey-xxx" {
		t.Errorf("secret not stored: %q", got)
	}
	s, _ := store.Load()
	if s.Modelserver.ProjectID != "proj-1" {
		t.Errorf("project id = %q", s.Modelserver.ProjectID)
	}
	if s.Modelserver.APIKeySuffix != "wxyz" {
		t.Errorf("key suffix = %q", s.Modelserver.APIKeySuffix)
	}
	if !s.Onboarding.HasCompleted("modelserver_login") {
		t.Errorf("step not marked completed")
	}
}
```

- [ ] **Step 4: Run it**

Run: `cd /root/agentserver-pkg && go test ./internal/ui/ -run TestPollModelserverLogin_FullPKCE -v -race`
Expected: PASS.

- [ ] **Step 5: Run all UI tests**

Run: `cd /root/agentserver-pkg && go test ./internal/ui/ -v -race`
Expected: All PASS.

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver-pkg
git add internal/ui/orchestrator_real_test.go
git commit -m "test(ui): cover LoginModelserver listener+browser and full PKCE flow"
```

---

## Task 13: Update front-end `app.js` to split MS / AS branches

**Files:**
- Modify: `internal/ui/assets/app.js` (replace lines 55-63)

- [ ] **Step 1: Replace the combined OAuth branch with two branches**

Edit `internal/ui/assets/app.js`. Find:

```js
} else if (s.id === 'modelserver_login' || s.id === 'agentserver_login') {
  const ch = await fetchJSON('/api/step/' + s.id, { method: 'POST' });
  alert('请在弹出的浏览器中完成登录。\n用户码: ' + ch.user_code);
  // Poll until success
  while (true) {
    const st = await fetchJSON('/api/step/' + s.id + '/status');
    if (st.state === 'success') break;
    await new Promise(r => setTimeout(r, 3000));
  }
}
```

Replace with:

```js
} else if (s.id === 'modelserver_login') {
  await fetchJSON('/api/step/' + s.id, { method: 'POST' });
  // PKCE: no user_code to show; browser opens silently. Poll until done.
  while (true) {
    const st = await fetchJSON('/api/step/' + s.id + '/status');
    if (st.state === 'success') break;
    if (st.error) { alert('登录失败: ' + st.error); return; }
    await new Promise(r => setTimeout(r, 3000));
  }
} else if (s.id === 'agentserver_login') {
  const ch = await fetchJSON('/api/step/' + s.id, { method: 'POST' });
  alert('请在弹出的浏览器中完成登录。\n用户码: ' + ch.user_code);
  // Poll until success
  while (true) {
    const st = await fetchJSON('/api/step/' + s.id + '/status');
    if (st.state === 'success') break;
    await new Promise(r => setTimeout(r, 3000));
  }
}
```

- [ ] **Step 2: Sanity check — UI assets are embedded via `//go:embed`**

Run: `cd /root/agentserver-pkg && go build ./internal/ui/`
Expected: BUILD PASS.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver-pkg
git add internal/ui/assets/app.js
git commit -m "feat(ui): split MS/AS branches; MS shows spinner (no user_code)"
```

---

## Task 14: Wire new config into `cmd/launcher/main.go`

**Files:**
- Modify: `cmd/launcher/main.go` (lines 60-104)

- [ ] **Step 1: Replace `msOAuth` and add `OpenBrowser` to Deps**

Edit `cmd/launcher/main.go`. Find the `msOAuth :=` block (around line 64) and replace it:

```go
	// modelserver: authorization_code + PKCE, public client registered by
	// ops on 2026-06-03 (see docs/ops/modelserver-oauth-client-registration.md).
	// 8 fixed callback ports because ops registered explicit redirect_uris
	// rather than wildcard 127.0.0.1.
	msOAuth := oauth.AuthCodeConfig{
		Endpoint:     "https://codeapi.cs.ac.cn",
		AuthPath:     "/oauth2/auth",
		TokenPath:    "/oauth2/token",
		ClientID:     "5321f7e6-3d79-4ac9-a742-04809dbf9025",
		Scope:        "project:inference offline_access",
		CallbackPath: "/oauth/modelserver/callback",
		Ports:        []int{53428, 53429, 53430, 53431, 53432, 53433, 53434, 53435},
		// LoginTimeout: 0 → defaults to 10 * time.Minute in StartListening
	}
```

Leave `asOAuth` untouched.

Find the `deps := ui.Deps{` block (around line 89-104) and add the `OpenBrowser` field:

```go
	deps := ui.Deps{
		State:             store,
		Secrets:           sec,
		MS:                modelserver.New("https://code.cs.ac.cn"),
		AS:                agentserver.New("https://agent.cs.ac.cn"),
		MSOAuth:           msOAuth,
		ASOAuth:           asOAuth,
		OpenBrowser:       func(url string) { _ = browser.Open(url) },
		CodexConfigPath:   p.CodexConfigFile,
		VSCodeUserDataDir: p.VSCodeUserDataDir,
		VSCodeExtDir:      p.VSCodeExtDir,
		EmbeddedVSIXPath:  joinExe(installDir, "agentserver-vscode.vsix"),
		CodexAbsPath:      p.CodexExePath,
		LauncherExePath:   joinExe(installDir, "launcher.exe"),
		OpenFolderExePath: joinExe(installDir, "open-folder.exe"),
		IconPath:          joinExe(installDir, "icon.ico"),
	}
```

Find the local `openBrowser` declaration that's already in main (line ~106). Leave it as-is — `ui.NewServer` still uses it for AS `verification_uri_complete` and for the initial onboarding URL.

- [ ] **Step 2: Build the launcher**

Run: `cd /root/agentserver-pkg && go build ./cmd/launcher/`
Expected: BUILD PASS.

- [ ] **Step 3: Build everything**

Run: `cd /root/agentserver-pkg && make build` (or `go build ./...`)
Expected: BUILD PASS.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver-pkg
git add cmd/launcher/main.go
git commit -m "feat(launcher): wire PKCE AuthCodeConfig + OpenBrowser into Deps"
```

---

## Task 15: Extend fakeserver to emulate Hydra /oauth2/auth + authorization_code token

**Files:**
- Modify: `test/integration/fakeserver/fakeserver.go`

- [ ] **Step 1: Add `/oauth2/auth` handler and extend `/api/oauth2/token`**

Edit `test/integration/fakeserver/fakeserver.go`. Add a new handler and adjust the existing one:

```go
// In Start(), after the existing modelserver routes, add:
mux.HandleFunc("/oauth2/auth", s.handlePKCEAuth)        // PKCE: GET, redirects to callback
mux.HandleFunc("/oauth2/token", s.handlePKCETokenSwap)  // PKCE: POST authorization_code
```

(Keep the existing `/api/oauth2/device/auth` and `/api/oauth2/token` for the
device-code path still used by agentserver. Putting `/oauth2/token` at a
different path avoids collision.)

Now add the two handlers at the bottom of the file:

```go
// handlePKCEAuth emulates Hydra's /oauth2/auth: 302 immediately to the
// redirect_uri with a fixed code + the state echoed back. Tests can also
// have the installer's callback HTML page get served (by the installer's
// own listener) when the browser follows the redirect — but our integration
// tests don't drive a real browser; the fake server's job is just to
// produce the redirect.
//
// We don't redirect — instead we directly issue an HTTP GET to the
// installer's callback URL ourselves, simulating what a real browser would
// do. This keeps the test deterministic.
func (s *Server) handlePKCEAuth(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	if redirectURI == "" || state == "" {
		http.Error(w, "missing redirect_uri or state", 400)
		return
	}
	// Spin off a goroutine that GETs the installer's callback URL with a
	// fixed code, mimicking the browser following Hydra's 302.
	go func() {
		_, _ = http.Get(fmt.Sprintf("%s?code=fake-pkce-code&state=%s",
			redirectURI, state))
	}()
	w.WriteHeader(204) // No content; the fake browser is "elsewhere".
}

// handlePKCETokenSwap emulates Hydra's /oauth2/token for grant_type=authorization_code.
// Returns a fixed access_token regardless of code/verifier (tests don't crypto-verify).
func (s *Server) handlePKCETokenSwap(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.PostForm.Get("grant_type") != "authorization_code" {
		http.Error(w, "wrong grant_type", 400)
		return
	}
	if r.PostForm.Get("code") == "" || r.PostForm.Get("code_verifier") == "" {
		http.Error(w, "missing code or code_verifier", 400)
		return
	}
	writeJSON(w, 200, map[string]any{
		"access_token": "fake-pkce-at", "token_type": "Bearer", "expires_in": 3600,
		"refresh_token": "fake-pkce-rt",
	})
}
```

But wait — the installer drives `OpenBrowser` to actually open Hydra's
`/oauth2/auth` URL, which here is the fake server. In integration tests we
don't run a real browser, so we inject a custom `OpenBrowser` (Task 16
changes the integration test setup). The fake's `/oauth2/auth` handler still
needs to exist so the integration test's `OpenBrowser` can hit it (it will
HTTP GET the auth URL, and the fake will then HTTP GET back to the
installer's callback URL). This double-bounce avoids needing a real browser.

- [ ] **Step 2: Build the fakeserver**

Run: `cd /root/agentserver-pkg && go build -tags integration ./test/integration/fakeserver/`
Expected: BUILD PASS.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver-pkg
git add test/integration/fakeserver/fakeserver.go
git commit -m "test(integration): fake Hydra PKCE endpoints in fakeserver"
```

---

## Task 16: Update 3 integration tests to use `AuthCodeConfig`

**Files:**
- Modify: `test/integration/flows/full_onboarding_test.go`
- Modify: `test/integration/flows/resume_test.go`
- Modify: `test/integration/flows/idempotent_test.go`

- [ ] **Step 1: Switch `MSOAuth` to `AuthCodeConfig` in all three tests**

For each file, find the `MSOAuth: oauth.Config{...}` block and replace it
with `AuthCodeConfig`. Add the `OpenBrowser` to `Deps` (and ensure each test
gets a free port for the callback).

**`test/integration/flows/full_onboarding_test.go`** — replace the existing
`MSOAuth: oauth.Config{...}` and add an `OpenBrowser` shim that drives the
fake server to issue the callback:

```go
// At top of the test function, before building deps, allocate a port:
l, _ := net.Listen("tcp", "127.0.0.1:0")
msPort := l.Addr().(*net.TCPAddr).Port
l.Close()

// Replace MSOAuth (was oauth.Config{...}) with:
MSOAuth: oauth.AuthCodeConfig{
	Endpoint:     fake.MSURL(),
	AuthPath:     "/oauth2/auth",
	TokenPath:    "/oauth2/token",
	ClientID:     "test",
	Scope:        "project:inference offline_access",
	CallbackPath: "/oauth/modelserver/callback",
	Ports:        []int{msPort},
	LoginTimeout: 3 * time.Second,
},

// Add OpenBrowser into Deps (drives the auth URL → fake server triggers
// callback automatically per Task 15):
OpenBrowser: func(url string) { _, _ = http.Get(url) },
```

Add `"net"` and `"time"` to imports if not already there.

**`test/integration/flows/resume_test.go`** — same pattern:

```go
l, _ := net.Listen("tcp", "127.0.0.1:0")
msPort := l.Addr().(*net.TCPAddr).Port
l.Close()

// Replace MSOAuth:
MSOAuth: oauth.AuthCodeConfig{
	Endpoint:     fake.MSURL(),
	AuthPath:     "/oauth2/auth",
	TokenPath:    "/oauth2/token",
	ClientID:     "test",
	Scope:        "project:inference offline_access",
	CallbackPath: "/oauth/modelserver/callback",
	Ports:        []int{msPort},
	LoginTimeout: 3 * time.Second,
},

// Add OpenBrowser:
OpenBrowser: func(url string) { _, _ = http.Get(url) },
```

**`test/integration/flows/idempotent_test.go`** — same.

Note: the test runs MS login and then re-runs it after a kill — the second
run will pick the same single-port `msPort`. By that point the prior
listener is gone (orchestrator was destroyed with the killed process), so
the port is free.

- [ ] **Step 2: Build integration tests**

Run: `cd /root/agentserver-pkg && go test -tags integration -count=0 ./test/integration/flows/...`
Expected: BUILD PASS (no actual run with `-count=0`).

- [ ] **Step 3: Run integration tests**

Run: `cd /root/agentserver-pkg && go test -tags integration -race ./test/integration/flows/... -v`
Expected: All PASS — `TestFullOnboarding_MS_AS`, `TestResumeAfterKill_*`, `TestIdempotentWorkspace_*`.

If they hang on MS login: the fake's `/oauth2/auth` may not be triggering
the callback — verify the goroutine in `handlePKCEAuth` (Task 15) is using
the `redirect_uri` the installer passed, including the correct port.

- [ ] **Step 4: Run the full test suite (unit + integration)**

Run: `cd /root/agentserver-pkg && go test -race ./... && go test -tags integration -race ./test/integration/...`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver-pkg
git add test/integration/flows/full_onboarding_test.go test/integration/flows/resume_test.go test/integration/flows/idempotent_test.go
git commit -m "test(integration): switch MS login to PKCE AuthCodeConfig"
```

---

## Task 17: Rewrite the ops doc as v6 "已完成"

**Files:**
- Modify: `docs/ops/modelserver-oauth-client-registration.md`

- [ ] **Step 1: Replace the whole file**

Overwrite `docs/ops/modelserver-oauth-client-registration.md`:

```markdown
# modelserver OAuth 客户端 — 已完成 ✅

(2026-06-03 v6 — 运维已交付如下 client, 安装包已对接, 全程跑通)

## 交付的 client (运维 2026-06-03 07:41 UTC 创建)

| 字段 | 值 |
|---|---|
| Client ID | `5321f7e6-3d79-4ac9-a742-04809dbf9025` |
| Token Endpoint Auth Method | `none` (public client, 用 PKCE 证身份) |
| Grant Types | `authorization_code`, `refresh_token` (额外 `device_code` 未使用) |
| Response Types | `code` |
| Scope | `project:inference offline_access` |
| Callback Path | `/oauth/modelserver/callback` |
| Redirect URIs (8 个固定端口) | `http://127.0.0.1:53428..53435/oauth/modelserver/callback` |

代码: `cmd/launcher/main.go` 的 `msOAuth := oauth.AuthCodeConfig{...}`。

## 完整链路 (8 步)

```
1. 用户双击桌面快捷方式 agentserver-vscode
2. 安装包: 本地 127.0.0.1:<53428..53435 第一个空闲> listen
3. 安装包: 浏览器打开
     https://codeapi.cs.ac.cn/oauth2/auth
       ?response_type=code
       &client_id=5321f7e6-3d79-4ac9-a742-04809dbf9025
       &redirect_uri=http://127.0.0.1:<port>/oauth/modelserver/callback
       &scope=project:inference%20offline_access
       &code_challenge=<sha256(verifier)>
       &code_challenge_method=S256
       &state=<nonce>
4. 用户在 Hydra 微信扫码登录、同意 (跟 dashboard 同一套 UI)
5. Hydra 302 → http://127.0.0.1:<port>/oauth/modelserver/callback?code=...&state=...
6. 安装包: POST https://codeapi.cs.ac.cn/oauth2/token
       grant_type=authorization_code
       code=<上一步拿到的>
       code_verifier=<开头生成的>         ← public 客户端的身份证明
       client_id=5321f7e6-3d79-4ac9-a742-04809dbf9025
       redirect_uri=http://127.0.0.1:<port>/oauth/modelserver/callback
   ← {access_token, refresh_token, expires_in}

7. 安装包: 拿 access_token 调 modelserver admin API:
       POST https://code.cs.ac.cn/api/v1/projects                  → 建/选 "default" project
       POST https://code.cs.ac.cn/api/v1/projects/{id}/keys        → 生成 ms-xxxxxx... 长期 API key

8. 安装包: 写 keyring (modelserver_api_key) + setx OPENAI_API_KEY=...
          写 ~/.codex/config.toml: base_url=https://code.ai.cs.ac.cn/v1, env_key=OPENAI_API_KEY
```

模型 server admin API 接受 OAuth Bearer 是因为
`internal/proxy/auth_middleware.go:148-282` 会在 API key 验证失败后 fallback 到
token introspection。

## 请回滚 (历史遗留)

如果之前按 v2/v3/v4 文档做过下面这些, 请回滚 — 当前方案不需要:

- **Hydra `urls.device.verification`** — 删掉, 留着会让 `/oauth2/device/verify` 进 302 死循环
- **modelserver config `auth.oauth.hydra.device_flow.{client_id,client_secret}`** — 删掉, 当前方案不走 modelserver wrapper

## 如果以后需要调整

| 想改什么 | 改哪 |
|---|---|
| 换 client_id | `cmd/launcher/main.go` 的 `msOAuth.ClientID` |
| 加/换 callback 端口 | `cmd/launcher/main.go` 的 `msOAuth.Ports` + 运维更新 redirect_uris 列表 |
| 改 scope | `cmd/launcher/main.go` 的 `msOAuth.Scope` |
| 改登录超时 | `cmd/launcher/main.go` 的 `msOAuth.LoginTimeout` (默认 10 分钟) |
| 改回调 path | `cmd/launcher/main.go` 的 `msOAuth.CallbackPath` + 运维更新 redirect_uris |
```

- [ ] **Step 2: Commit**

```bash
cd /root/agentserver-pkg
git add docs/ops/modelserver-oauth-client-registration.md
git commit -m "docs(ops): v6 — record delivered PKCE client + full chain"
```

---

## Task 18: Final verification — full suite

**Files:** none

- [ ] **Step 1: Run the full unit suite with race detector**

Run: `cd /root/agentserver-pkg && go test -race ./...`
Expected: All PASS.

- [ ] **Step 2: Run the integration suite**

Run: `cd /root/agentserver-pkg && go test -tags integration -race ./test/integration/...`
Expected: All PASS.

- [ ] **Step 3: Build all binaries**

Run: `cd /root/agentserver-pkg && make build`
Expected: BUILD PASS for `launcher`, `onboarding-server`, `agentctl`, `open-folder`.

- [ ] **Step 4: Cross-compile Windows binaries**

Run: `cd /root/agentserver-pkg && make cross-windows`
Expected: BUILD PASS for all four Windows .exe binaries.

- [ ] **Step 5: Sanity check — `go vet`**

Run: `cd /root/agentserver-pkg && go vet ./...`
Expected: No reports.

- [ ] **Step 6: Final commit (if anything was tweaked) or skip**

If any step 1-5 surfaced a fix, commit it now:

```bash
cd /root/agentserver-pkg
git add -A
git commit -m "fix: address final verification issues"
```

Otherwise, no commit needed — work is done.

---

## Self-Review

**Spec coverage** (cross-checked against `docs/superpowers/specs/2026-06-03-modelserver-pkce-login-design.md`):

| Spec item | Implemented in |
|---|---|
| `AuthCodeConfig` struct | Task 1 |
| `PKCESession` struct | Task 1 |
| `StartPKCE` with S256 | Tasks 1, 2 |
| `FinishPKCE` token exchange | Task 3 |
| `FinishPKCE` invalid_grant handling | Task 4 |
| `ReservePort` + `ErrAllPortsBusy` | Task 5 |
| 4 embedded Chinese HTML pages | Task 6 |
| `CallbackResult` + `StartListening` | Task 7 |
| StartListening 4 error paths + ctx cancel | Task 8 |
| `Orchestrator` interface change | Task 9 |
| `Deps.OpenBrowser` field + receiver fields | Task 9 |
| `LoginModelserver` PKCE rewrite | Task 10 |
| `PollModelserverLogin` PKCE rewrite | Task 10 |
| `cleanupMS` helper | Task 10 |
| `handleMSLogin` simplification | Task 11 |
| `TestServerStepEndpoint` updated | Task 11 |
| Test 12: `TestLoginModelserver_StartsListenerOpensBrowser` | Task 12 |
| Test 13: `TestPollModelserverLogin_FullPKCE` | Task 12 |
| `app.js` MS/AS split | Task 13 |
| `cmd/launcher/main.go` config | Task 14 |
| Fakeserver PKCE endpoints | Task 15 |
| 3 integration tests switched | Task 16 |
| Ops doc v6 | Task 17 |
| Final verification | Task 18 |

✅ All spec items covered. Spec tests 1-11 are implemented; tests 12-13 are
the orchestrator pair; agentserver tests are untouched as required.

**Placeholder scan**: No TBD/TODO/FIXME/XXX in any task. All steps have
exact file paths, complete code blocks where code is asked for, exact
commands, and expected outputs.

**Type consistency**: cross-checked these names appear identically wherever
referenced:
- `AuthCodeConfig` (Tasks 1, 5, 9, 14, 16) ✓
- `PKCESession` (Tasks 1, 7, 9, 10) ✓
- `StartPKCE` (Tasks 1, 2, 10) ✓
- `FinishPKCE` (Tasks 3, 4, 10) ✓
- `ReservePort` (Tasks 5, 7, 10) ✓
- `ErrAllPortsBusy` (Tasks 5, 10) ✓
- `CallbackResult` (Tasks 7, 8, 9) ✓
- `StartListening` (Tasks 7, 8, 10) ✓
- `cleanupMS` (Task 10) ✓
- `LoginModelserver` returns `error` (Tasks 9, 10, 11, 12) ✓
- `PollModelserverLogin` returns `(modelserver.APIKey, error)` (Tasks 9, 10, 12) ✓
- `Deps.OpenBrowser` (Tasks 9, 12, 14, 16) ✓
- `Deps.MSOAuth oauth.AuthCodeConfig` (Tasks 9, 14, 16) ✓
- `CallbackPath` consistently `/oauth/modelserver/callback` (Tasks 1, 12, 14, 16, 17) ✓
- Port range `53428..53435` (Tasks 14, 17) ✓
- Client ID `5321f7e6-3d79-4ac9-a742-04809dbf9025` (Tasks 12, 14, 17) ✓

✅ Names consistent across tasks.
