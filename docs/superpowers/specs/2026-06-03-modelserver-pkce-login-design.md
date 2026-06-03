# Modelserver login: switch from device flow to authorization_code + PKCE

**Date**: 2026-06-03
**Status**: Design approved, ready for implementation plan
**Scope**: Only the modelserver login path (`LoginModelserver` / `PollModelserverLogin`).
The agentserver path stays on device code.

## Motivation

### Where we started

The installer (`cmd/launcher`) drives two OAuth logins through `internal/oauth/`:

- **modelserver** — current code calls `RequestDeviceCode` against
  `https://code.cs.ac.cn/oauth/device/code`, returns a `DeviceCodeChallenge`
  with `user_code`/`verification_uri_complete`, opens the verification URI in
  the user's browser, then polls `/oauth/device/token`.
- **agentserver** — same shape against `https://agent.cs.ac.cn/api/oauth2/device/auth`.

For modelserver, `/oauth/device/code` returns 404 because modelserver's
`internal/admin/routes.go:52` only registers the `/oauth/device/*` routes when
`cfg.Auth.OAuth.Hydra.DeviceFlow.ClientID` is set. That switch is part of a
modelserver-server-side wrapper (`internal/admin/device_flow.go`) that — despite
exposing RFC 8628-shaped endpoints — internally drives `authorization_code`
against Hydra using its own confidential client. Turning it on requires:

1. Ops creating a confidential client (with `client_secret`) in Hydra
2. Ops adding both `client_id` and `client_secret` to modelserver's `config.yml`
3. Restarting modelserver

That's two repos and one config rollout for ops just to enable a flow the
installer doesn't structurally need.

### Why PKCE is the right fit

RFC 8252 §8.1 ("OAuth 2.0 for Native Apps") explicitly recommends
authorization_code + PKCE + loopback redirect for desktop apps with a local
browser. Device flow is designed for clients that **can't open a browser**
(TVs, headless CLIs, IoT). Our installer is a desktop GUI on Windows — the
user already has a browser, and walking them through "see this code, open
this URL, paste the code" is more friction than "browser pops, scan WeChat,
done".

PKCE also collapses the ops change into **one Hydra client registration**:
no `client_secret`, no modelserver config change, no service restart.

### What we got from ops (2026-06-03)

```json
{
  "client_id": "5321f7e6-3d79-4ac9-a742-04809dbf9025",
  "redirect_uris": [
    "http://127.0.0.1:53428/oauth/modelserver/callback",
    "http://127.0.0.1:53429/oauth/modelserver/callback",
    "http://127.0.0.1:53430/oauth/modelserver/callback",
    "http://127.0.0.1:53431/oauth/modelserver/callback",
    "http://127.0.0.1:53432/oauth/modelserver/callback",
    "http://127.0.0.1:53433/oauth/modelserver/callback",
    "http://127.0.0.1:53434/oauth/modelserver/callback",
    "http://127.0.0.1:53435/oauth/modelserver/callback"
  ],
  "grant_types": ["authorization_code", "refresh_token",
                  "urn:ietf:params:oauth:grant-type:device_code"],
  "response_types": ["code"],
  "scope": "project:inference offline_access",
  "token_endpoint_auth_method": "none"
}
```

Note that ops gave us 8 fixed ports instead of the wildcard `127.0.0.1`
loopback (RFC 8252 §7.3) we asked for. The installer must pick one of these
8 ports per login attempt. The extra `device_code` grant on the client is
unused by us — harmless. No `client_secret` (it's a public client; PKCE proves
caller identity).

## Architecture

### Scope of change

```
internal/oauth/
  devicecode.go              unchanged (still used by agentserver path)
  types.go                   unchanged (Config + DeviceCodeChallenge + Token kept)
+ authcode_pkce.go           new: PKCE protocol layer (verifier/challenge/URL/exchange)
+ pkce_callback.go           new: local HTTP listener (port reservation + callback decode)
+ authcode_pkce_test.go      new: S256 verifier, URL build, token exchange
+ pkce_callback_test.go      new: port fallback, callback decode, ctx cancel

internal/ui/
  orchestrator.go            changed: Orchestrator.LoginModelserver returns error (was DeviceCodeChallenge)
  orchestrator_real.go       changed: Deps gains OpenBrowser + MSOAuth typed AuthCodeConfig;
                                       realOrchestrator gains msSession/msCallback/msShutdown;
                                       LoginModelserver/PollModelserverLogin rewritten
  orchestrator_real_test.go  changed: 2 tests rewritten for PKCE; agentserver tests untouched
  server.go                  changed: handleMSLogin no longer returns ch / opens browser
  assets/app.js              changed: modelserver_login branch shows spinner (no user_code alert);
                                       agentserver_login branch unchanged

cmd/launcher/main.go         changed: msOAuth re-typed to oauth.AuthCodeConfig;
                                       deps.OpenBrowser assigned

docs/ops/modelserver-oauth-client-registration.md
                             changed: v6 - records delivered client_id + 8 ports;
                                       drops "ops to-do" sections (they're done)
```

### Invariants

- **agentserver path completely untouched**. `asOAuth`, `LoginAgentserver`,
  `PollAgentserverLogin`, `internal/oauth/devicecode.go`, the agentserver
  branch in `app.js`, all tests for the agentserver path — zero changes.
- **modelserver admin API call chain unchanged**.
  `MS.PickOrCreateProject` → `MS.CreateAPIKey` → `Secrets.Set` → `State.Update`
  is reused as-is. The access token coming out of PKCE is interchangeable with
  the one device flow used to produce, because `modelserver/internal/proxy/auth_middleware.go:148-282`
  accepts any active Hydra OAuth Bearer token via introspection fallback.
- **UI two-step shape preserved**. `POST /api/step/modelserver_login` (Login)
  + `GET /api/step/modelserver_login/status` (Poll) — same surface, different
  semantics inside.

## Components

### `internal/oauth/authcode_pkce.go` — protocol layer

Pure functions; no network listeners, no goroutines, no globals.

```go
type AuthCodeConfig struct {
    Endpoint       string        // "https://codeapi.cs.ac.cn"
    AuthPath       string        // "/oauth2/auth"
    TokenPath      string        // "/oauth2/token"
    ClientID       string        // "5321f7e6-3d79-4ac9-a742-04809dbf9025"
    Scope          string        // "project:inference offline_access"
    CallbackPath   string        // "/oauth/modelserver/callback"
    Ports          []int         // [53428..53435]
    LoginTimeout   time.Duration // default 10*time.Minute
}

// PKCESession is one in-flight login attempt.
// Constructed by StartPKCE with the chosen redirectURI; consumed by FinishPKCE.
type PKCESession struct {
    Verifier    string  // 64-byte base64url (43-128 chars per RFC 7636)
    Challenge   string  // base64url(sha256(verifier))
    State       string  // 16-byte base64url CSRF nonce
    RedirectURI string  // http://127.0.0.1:<port>/oauth/modelserver/callback
    AuthURL     string  // pre-built URL to open in browser
}

// StartPKCE generates verifier/challenge/state and pre-builds the auth URL.
// Caller MUST have already reserved a port (via ReservePort) and passed the
// resulting redirectURI here. AuthURL embeds redirectURI, so reordering would
// require rebuilding the URL.
func StartPKCE(cfg AuthCodeConfig, redirectURI string) (*PKCESession, error)

// FinishPKCE exchanges the auth code for a Token, sending code_verifier
// rather than a client_secret (public client).
func FinishPKCE(ctx context.Context, cfg AuthCodeConfig, sess *PKCESession, code string) (Token, error)
```

### `internal/oauth/pkce_callback.go` — listener layer

Owns the local HTTP server, knows nothing about OAuth semantics beyond
"read code and state from query string".

```go
type CallbackResult struct {
    Code  string  // present on success
    State string  // echoed back; caller verifies match
    Error string  // OAuth error code if user refused (access_denied, etc.)
}

// ReservePort tries cfg.Ports in order, returns the first that net.Listen accepts.
// The returned listener is held for StartListening; caller MUST hand it off
// or .Close() it.
func ReservePort(cfg AuthCodeConfig) (port int, ln net.Listener, err error)

// StartListening serves cfg.CallbackPath on ln. The handler:
//   - matches request.URL.Query().Get("state") against expectedState
//     (mismatch → serve state_mismatch.html, DO NOT send on channel)
//   - on error= → send CallbackResult{Error:...}, serve denied.html
//   - on code= → send CallbackResult{Code, State}, serve success.html
//   - on neither → serve missing_code.html, DO NOT send
// The channel receives at most one value; closed by shutdown().
// Caller MUST call shutdown(); idempotent.
func StartListening(ctx context.Context, ln net.Listener, cfg AuthCodeConfig, expectedState string) (
    ch <-chan CallbackResult, shutdown func(),
)

// ErrAllPortsBusy is returned by ReservePort when no cfg.Ports can be bound.
var ErrAllPortsBusy = errors.New("all configured callback ports are busy")
```

The two-step split (ReservePort + StartListening) is intentional: the
orchestrator needs to know the port to construct `redirectURI`, which feeds
into `StartPKCE` which produces `state`, which is the input to `StartListening`.
Without the split we'd need an atomic late-binding of `expectedState` into the
handler.

### Callback HTML pages

Four pages, embedded via `//go:embed templates/*.html`:

| File | Shown when | Channel sent? |
|---|---|---|
| `success.html` | code present, state matches | yes (Code, State) |
| `denied.html` | `?error=...` | yes (Error) |
| `state_mismatch.html` | state mismatch or missing | no — caller times out |
| `missing_code.html` | no code and no error | no — caller times out |

Each ~10 lines, Chinese text, inline `<style>`. No JS, no external assets.

### `internal/ui/orchestrator.go` — interface change

```go
LoginModelserver(ctx context.Context) error
//   Returns nil on success: listener up, PKCE session armed, browser opened.
//   ErrAllPortsBusy surfaces as a wrapped error.
//   Caller then polls PollModelserverLogin until terminal state.

PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error)
//   Blocks until callback arrives OR ctx cancels.
//   On callback success: exchanges code for token, builds project + API key,
//   persists to secrets + state, returns key.
//   On callback error (access_denied / state mismatch / token exchange fail):
//     cleans up listener and returns error (terminal — user must retry from
//     LoginModelserver).
//   On ctx cancel: returns ctx.Err() WITHOUT cleanup — caller is expected
//     to re-poll. (The HTTP server.handleMSStatus wraps Poll in a 30s timeout
//     so the front end can keep its 3s-poll UX without holding a 10-minute
//     HTTP connection.)
```

`noopOrchestrator.LoginModelserver` becomes `func(context.Context) error { return nil }`.

### `internal/ui/orchestrator_real.go` — state + methods

```go
type Deps struct {
    // ... existing fields ...
    MSOAuth     oauth.AuthCodeConfig    // CHANGED type from oauth.Config
    ASOAuth     oauth.Config            // unchanged
    OpenBrowser func(string)            // NEW: previously implicit via ui.Server
}

type realOrchestrator struct {
    d           Deps
    // Modelserver PKCE in-flight session (replaces msChallenge):
    msSession   *oauth.PKCESession
    msCallback  <-chan oauth.CallbackResult
    msShutdown  func()
    // Agentserver device-code in-flight challenge (unchanged):
    asChallenge oauth.DeviceCodeChallenge
    msToken     oauth.Token
    asToken     oauth.Token
}
```

`LoginModelserver` flow:
1. `ReservePort(cfg)` — `ErrAllPortsBusy` short-circuits with user-facing
   error message naming the port range
2. Build `redirectURI` = `http://127.0.0.1:<port><callbackPath>`
3. `StartPKCE(cfg, redirectURI)` → session with verifier+state+AuthURL
4. `StartListening(ctx, ln, cfg, session.State)` → channel + shutdown
5. Store session/channel/shutdown on receiver
6. `go d.OpenBrowser(session.AuthURL)`
7. Return nil

`PollModelserverLogin` flow:
1. If `msSession == nil` → error "no in-flight login"
2. `select` on channel and `ctx.Done()`
3. On channel:
   - `Error != ""` → cleanup + return user-friendly error (terminal)
   - `State != session.State` → cleanup + return "session mismatch" (terminal)
   - else: run the post-callback chain
     `FinishPKCE(ctx, cfg, session, res.Code)` → token →
     `MS.PickOrCreateProject` → `MS.CreateAPIKey` → `Secrets.Set` →
     `State.Update` → cleanup + return key.
     **Any error from any step in this chain → cleanup + return error**
     (terminal — the callback's `code` is single-use; user must restart from
     LoginModelserver).
4. On `ctx.Done()` → return `ctx.Err()` (NO cleanup; caller re-polls)

`cleanupMS()`: calls `msShutdown` (if non-nil), nils all three msSession/
msCallback/msShutdown fields. Idempotent.

### `internal/ui/server.go` — handler simplification

```go
func (s *server) handleMSLogin(w http.ResponseWriter, r *http.Request) {
    if err := s.o.LoginModelserver(r.Context()); err != nil {
        writeErr(w, 500, err); return
    }
    writeJSON(w, 200, map[string]string{"state": "started"})
}
```

No more `s.openBrowser(ch.VerificationURIComplete)` for modelserver — the
orchestrator owns browser-opening now. `handleMSStatus` is unchanged (still
wraps Poll in a 30s timeout and returns `{"state":"waiting"}` on timeout).
`handleASLogin` and `handleASStatus` unchanged.

`NewServer(o, openBrowser)` signature unchanged — `openBrowser` is still used
for the initial onboarding URL and the agentserver `verification_uri_complete`.

### `internal/ui/assets/app.js` — split the OAuth branch

```js
} else if (s.id === 'modelserver_login') {
  await fetchJSON('/api/step/' + s.id, { method: 'POST' });
  // PKCE: no user_code to show; browser opens silently. Just poll.
  while (true) {
    const st = await fetchJSON('/api/step/' + s.id + '/status');
    if (st.state === 'success') break;
    if (st.error) { alert('登录失败: ' + st.error); return; }
    await new Promise(r => setTimeout(r, 3000));
  }
} else if (s.id === 'agentserver_login') {
  const ch = await fetchJSON('/api/step/' + s.id, { method: 'POST' });
  alert('请在弹出的浏览器中完成登录。\n用户码: ' + ch.user_code);
  while (true) {
    const st = await fetchJSON('/api/step/' + s.id + '/status');
    if (st.state === 'success') break;
    await new Promise(r => setTimeout(r, 3000));
  }
}
```

### `cmd/launcher/main.go` — config

```go
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
// asOAuth unchanged

deps := ui.Deps{
    // ... existing fields ...
    MSOAuth:     msOAuth,
    ASOAuth:     asOAuth,
    OpenBrowser: func(url string) { _ = browser.Open(url) },  // NEW
}
```

## Data flow (happy path)

```
User clicks "登录 modelserver"
  │
  ↓
[front-end] POST /api/step/modelserver_login
  │
  ↓
[server.handleMSLogin] → orchestrator.LoginModelserver
  │
  ├─ ReservePort → port 53428 (or first free)
  ├─ StartPKCE(cfg, "http://127.0.0.1:53428/oauth/modelserver/callback")
  │     → session{Verifier, Challenge, State, RedirectURI, AuthURL}
  ├─ StartListening(ctx, ln, cfg, session.State) → callback channel + shutdown
  └─ go OpenBrowser(session.AuthURL)
  │
  ↓
[server] → 200 {"state":"started"}
[front-end] → start polling
  │
[browser] opens https://codeapi.cs.ac.cn/oauth2/auth?response_type=code&client_id=5321f7e6...
              &redirect_uri=http%3A%2F%2F127.0.0.1%3A53428%2Foauth%2Fmodelserver%2Fcallback
              &scope=project%3Ainference+offline_access&state=...&code_challenge=...
              &code_challenge_method=S256
[user] WeChat scan → consent
[Hydra] 302 → http://127.0.0.1:53428/oauth/modelserver/callback?code=...&state=...
[listener] handler:
  ├─ state matches → send {Code, State} on channel, serve success.html
[browser] shows "登录成功, 可关闭此页"
  │
[front-end] (concurrently polling) GET /api/step/modelserver_login/status
[server.handleMSStatus] ctx with 30s timeout → orchestrator.PollModelserverLogin
  │
  ├─ select on channel: receives {Code, State}
  ├─ State matches → FinishPKCE(ctx, cfg, session, Code)
  │     POST https://codeapi.cs.ac.cn/oauth2/token
  │       grant_type=authorization_code & code=... & code_verifier=...
  │       & client_id=5321f7e6... & redirect_uri=http://127.0.0.1:53428/...
  │     ← {access_token, refresh_token, expires_in}
  ├─ MS.PickOrCreateProject(ctx, token, "default") → project
  ├─ MS.CreateAPIKey(ctx, token, project.ID, "agentserver-vscode") → APIKey{Secret, KeySuffix}
  ├─ Secrets.Set("modelserver_api_key", key.Secret)
  ├─ State.Update: ProjectID, KeySuffix, mark step completed
  └─ cleanupMS() → return key
  │
[server] → 200 {"state":"success", "key_suffix":"xxxx"}
[front-end] → advance to next step
```

## Error handling

| Trigger | Surface | UX |
|---|---|---|
| All 8 ports occupied | `LoginModelserver` returns `ErrAllPortsBusy` wrapped with port range | front-end alert: "OAuth 回调端口 [53428..53435] 全部被占用, 请关闭其他 agentserver-vscode 进程后重试"; retry button rebinds |
| User declines on Hydra page | callback `?error=access_denied` → channel `{Error:"access_denied"}` | Poll returns "登录被拒绝: access_denied" immediately; retry → new session |
| User closes browser without acting | no callback arrives | StartListening's ctx (10min) expires → channel closed → Poll returns "登录会话已结束, 请重试" |
| State mismatch (replay / attack) | handler serves state_mismatch.html, does NOT send on channel | Poll waits until 10min ctx timeout, then "登录会话已结束"; browser shows the explicit Chinese error page |
| Token exchange fails (invalid_grant) | `FinishPKCE` returns wrapped error | Poll returns "授权码交换失败: ..."; cleanup; retry available |
| Listener bind succeeded but ctx cancels mid-flight | `select` hits `ctx.Done()` | Poll returns ctx.Err(); server handleMSStatus returns "waiting"; front-end re-polls 3s later; session still armed |
| `Abort()` | (existing) | calls cleanupMS to release port + listener |

10-minute upper bound on a single login attempt is enforced inside
`StartListening`, which derives `ctx, cancel := context.WithTimeout(ctx, cfg.LoginTimeout)`
(falling back to 10 min when `LoginTimeout == 0`) and closes the result channel
on expiry. Bounds the WeChat-scan-takes-forever case while leaving generous slack.
Tests inject a shorter `LoginTimeout` for fast assertions on timeout behavior.

## Testing

### `internal/oauth/authcode_pkce_test.go`

1. **`TestStartPKCE_S256`** — verifier ∈ [43,128] chars base64url; challenge =
   base64url(sha256(verifier)); state ≥ 16 bytes
2. **`TestStartPKCE_AuthURL`** — URL contains all of `response_type=code`,
   `client_id`, `redirect_uri` (escaped), `scope` (escaped), `state`,
   `code_challenge`, `code_challenge_method=S256`
3. **`TestFinishPKCE_Success`** — `httptest.Server` fakes `/oauth2/token`;
   verify POST body has `grant_type=authorization_code&code=...&code_verifier=...&client_id=...&redirect_uri=...`;
   decode token JSON
4. **`TestFinishPKCE_InvalidGrant`** — fake returns 400 `{"error":"invalid_grant"}`;
   verify error contains "invalid_grant"

### `internal/oauth/pkce_callback_test.go`

5. **`TestReservePort_FirstFree`** — Ports=[A,B,C], occupy A externally, verify
   ReservePort returns B
6. **`TestReservePort_AllBusy`** — occupy all ports externally, verify
   `errors.Is(err, ErrAllPortsBusy)`
7. **`TestStartListening_Success`** — ReservePort + StartListening("state-x"),
   GET `/oauth/modelserver/callback?code=foo&state=state-x`, verify channel
   receives `{Code:"foo", State:"state-x"}` and response body == success.html
8. **`TestStartListening_StateMismatch`** — same setup, state="wrong"; verify
   channel receives NOTHING within 100ms and response body == state_mismatch.html
9. **`TestStartListening_OAuthError`** — GET `?error=access_denied`; verify
   channel receives `{Error:"access_denied"}` and response body == denied.html
10. **`TestStartListening_MissingCode`** — GET with neither code nor error;
    verify channel receives NOTHING and response body == missing_code.html
11. **`TestStartListening_CtxCancel`** — start, send no request, cancel ctx;
    verify channel closes and shutdown() is safe to call

### `internal/ui/orchestrator_real_test.go` (2 tests rewritten)

12. **`TestLoginModelserver_StartsListenerOpensBrowser`** — inject fake
    OpenBrowser; call LoginModelserver; verify (a) listener is up on one of
    cfg.Ports, (b) OpenBrowser called once with URL containing
    `client_id=5321f7e6...`, (c) cleanup via Abort works
13. **`TestPollModelserverLogin_FullPKCE`** — end-to-end: LoginModelserver →
    simulate browser GET to chosen callback URL → PollModelserverLogin returns
    with key from fake modelserver client; verify Secrets has
    "modelserver_api_key" set and State has ProjectID + KeySuffix

Agentserver tests (`TestLoginAgentserver_*`, `TestPollAgentserverLogin_*`) —
**not touched**, agentserver path unchanged.

### `internal/ui/server_test.go`

No tests removed; `handleMSLogin` test updated to assert response is
`{"state":"started"}` instead of `DeviceCodeChallenge` shape.

## Out of scope (YAGNI)

- **Third-party OAuth library** (e.g. `golang.org/x/oauth2`) — current
  device-code is ~50 lines of `net/http`; PKCE will be similar. Adding an
  abstraction layer to swap two well-defined flows isn't worth the dependency.
- **`localhost` redirect URI** — RFC 8252 §8.3 says use the IP literal
  `127.0.0.1` to avoid DNS rebinding; ops registered the IP literal anyway.
- **Non-Chinese callback pages** — installer audience is Chinese users.
- **Persisting refresh_token** — current code uses the access token once
  (to create the long-lived `ms-` API key) and discards both. Refresh-token
  storage is a separate (future) feature; not part of this change.
- **Switching agentserver to PKCE** — agentserver's Hydra setup and its
  `agentserver-agent-cli` registration are designed for device flow. Touching
  it isn't in this scope; if we do it later it'll mirror this design.

## Docs update

`docs/ops/modelserver-oauth-client-registration.md` becomes v6:

- Header: "已完成 — ops 已交付如下 client, 代码已对接"
- One table with the delivered fields (client_id, callback path, port list,
  grant types, scope, auth method)
- Keep the "完整链路 (8 步)" diagram — useful for future maintenance
- Keep the "请回滚 / 不要做" section for the abandoned routes
  (Hydra `urls.device.verification`, modelserver `device_flow.client_id`)
- Add a short "如果以后需要调整" footer mapping (client / ports / scope) →
  (which file to edit)

## Risks and assumptions

- **8 fixed ports may not be enough** if the user has all 8 occupied (unlikely
  on a fresh Windows box). Mitigation: clear error message naming the range
  + retry. If we see this in the wild we can ask ops to widen.
- **Hydra strictly matches the callback URL string** including port; we never
  test this in isolation. If a port works in ReservePort but Hydra rejects
  the redirect_uri (e.g. because ops didn't actually register it), we'll see
  `error=invalid_redirect_uri` on the callback — handled by the OAuth error
  path. Should be caught by manual verification (curl) before merging.
- **Loopback callback works only if the browser and installer are on the same
  host**. True for our Windows installer scenario by definition; not a risk
  for v1.
- **10-minute login window** — assumes a user who started the flow finishes
  within that window. If not, they retry. Tunable.
