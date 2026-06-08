# Modelserver Logout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a console action that clears local modelserver login credentials so the user must reconnect the large-model service.

**Architecture:** Extend the existing console controller with a `LogoutModelserver` action that deletes modelserver access/refresh token secrets and marks modelserver reauth as required. Expose it through the onboarding HTTP server and call it from the Dashboard with a confirmation dialog, then refresh console state.

**Tech Stack:** Go HTTP/controller tests, Vue 3 Dashboard, Vitest, Element Plus.

---

### Task 1: Backend Logout Behavior

**Files:**
- Modify: `internal/console/state.go`
- Modify: `internal/console/state_test.go`

- [ ] **Step 1: Write the failing controller test**

Add a test that seeds `modelserver_api_key`, `modelserver_refresh_token`, `modelserver_access_token_expires_at`, refresh error flags, and a completed `modelserver_login` step, then calls `LogoutModelserver`. Assert the token secrets are gone, `modelserver_reauth_required=true` is set, and the completed step remains so the completed console can show the reconnect action.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/console -run TestControllerLogoutModelserverClearsLocalLogin -count=1`

Expected: FAIL because `LogoutModelserver` does not exist.

- [ ] **Step 3: Write minimal implementation**

Add `LogoutModelserver(ctx context.Context) error` to `console.Controller`. It should delete these secret keys: `modelserver_api_key`, `modelserver_refresh_token`, `modelserver_access_token_expires_at`, `modelserver_refresh_error`, `modelserver_refresh_error_at`. It should set `modelserver_reauth_required=true` so existing console state logic prompts the user to reconnect.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/console -run TestControllerLogoutModelserverClearsLocalLogin -count=1`

Expected: PASS.

### Task 2: HTTP API Endpoint

**Files:**
- Modify: `internal/ui/console.go`
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/server_test.go`

- [ ] **Step 1: Write failing server tests**

Add `/api/console/logout-modelserver` to the POST-only action endpoint test, and add a direct test that POSTing the endpoint calls the fake console controller.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui -run 'TestServerConsoleLogoutModelserverEndpoint|TestServerConsoleActionEndpointsRequirePost' -count=1`

Expected: FAIL because the endpoint and interface method do not exist.

- [ ] **Step 3: Write minimal implementation**

Add `LogoutModelserver(context.Context) error` to `ConsoleController` and `noopConsoleController`. Register `/api/console/logout-modelserver` in `NewServerWithConsole`, require POST, call the controller, and return `{"state":"logged_out"}`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui -run 'TestServerConsoleLogoutModelserverEndpoint|TestServerConsoleActionEndpointsRequirePost' -count=1`

Expected: PASS.

### Task 3: Frontend API and Dashboard

**Files:**
- Modify: `internal/ui/web/src/api.ts`
- Modify: `internal/ui/web/src/__tests__/api.spec.ts`
- Modify: `internal/ui/web/src/components/Dashboard.vue`
- Modify: `internal/ui/web/src/__tests__/Dashboard.spec.ts`

- [ ] **Step 1: Write failing frontend tests**

Add an API test that `logoutConsoleModelserver()` POSTs `/api/console/logout-modelserver`. Add a Dashboard test that confirms “退出大模型登录”, calls the API, and refreshes console state.

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- api.spec.ts Dashboard.spec.ts` in `internal/ui/web`.

Expected: FAIL because the API function and button do not exist.

- [ ] **Step 3: Write minimal implementation**

Add `logoutConsoleModelserver()` to `api.ts`. In Dashboard, add state for logout loading/error, add a confirmation dialog using `window.confirm`, call the API, refresh console state, and show a button labelled `退出大模型登录` near `打开订阅页`.

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- api.spec.ts Dashboard.spec.ts` in `internal/ui/web`.

Expected: PASS.

### Task 4: Verification

**Files:**
- Generated UI assets under `internal/ui/assets/dist`

- [ ] **Step 1: Build embedded UI assets if Dashboard changes pass tests**

Run the project’s existing UI build command from `internal/ui/web`, then ensure `internal/ui/assets/dist` updates.

- [ ] **Step 2: Run focused Go tests**

Run: `go test ./internal/console ./internal/ui -count=1`

Expected: PASS.

- [ ] **Step 3: Run broader verification**

Run: `go test ./... -count=1`, `npm test` in `internal/ui/web`, and `git diff --check`.

Expected: PASS.
