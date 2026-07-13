# Codex Direct Activation Contract Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Launch the official Store Codex package when `ActivateForProtocol` rejects its protocol contract, while retaining exact AUMID targeting and post-launch process verification.

**Architecture:** The preferred path remains `IApplicationActivationManager.ActivateForProtocol`. A small pure HRESULT predicate permits a fallback only for `0x80270254`; the fallback calls `ActivateApplication` on the same COM manager with `det.AppUserModelID` and the internally generated deep link. Existing `launchWithOptions` process-snapshot verification remains unchanged.

**Tech Stack:** Go, `golang.org/x/sys/windows`, Windows COM activation APIs, Go unit tests, Windows 11 acceptance host.

---

### Task 1: Pin the narrowly-scoped fallback contract with tests

**Files:**

- Modify: `internal/codexdesktop/launch_test.go`
- Modify: `internal/codexdesktop/launch_windows.go`

- [ ] **Step 1: Write the failing HRESULT-predicate test**

~~~go
func TestProtocolActivationContractFallbackIsRestricted(t *testing.T) {
	for _, tc := range []struct {
		name string
		hr   uintptr
		want bool
	}{
		{name: "observed unsupported contract", hr: 0x80270254, want: true},
		{name: "success", hr: 0, want: false},
		{name: "unrelated app-model failure", hr: 0x80073CF9, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := protocolActivationNeedsAppActivationFallback(tc.hr); got != tc.want {
				t.Fatalf("protocolActivationNeedsAppActivationFallback(0x%08X)=%t, want %t", uint32(tc.hr), got, tc.want)
			}
		})
	}
}
~~~

- [ ] **Step 2: Run it to prove RED**

Run `go test ./internal/codexdesktop -run '^TestProtocolActivationContractFallbackIsRestricted$' -count=1`.

Expected: compilation fails because `protocolActivationNeedsAppActivationFallback` does not exist.

- [ ] **Step 3: Extend the direct-activation source guard**

Extend `TestWindowsLaunchDirectlyActivatesDetectedAUMID` so its required source fragments include:

~~~go
"ActivateApplication",
"protocolActivationNeedsAppActivationFallback",
"det.AppUserModelID",
~~~

Keep its existing forbidden list (`browser.Open`, `browser.OpenContext`, `ShellExecute`, and `rundll32`) unchanged.

- [ ] **Step 4: Run it to prove RED**

Run `go test ./internal/codexdesktop -run '^TestWindowsLaunchDirectlyActivatesDetectedAUMID$' -count=1`.

Expected: it reports that the Windows source does not yet contain `ActivateApplication` and the fallback predicate.

### Task 2: Implement the direct-AUMID fallback

**Files:**

- Modify: `internal/codexdesktop/launch_windows.go`

- [ ] **Step 1: Add the exact-only predicate**

~~~go
const protocolActivationContractUnsupportedHRESULT uintptr = 0x80270254

func protocolActivationNeedsAppActivationFallback(hr uintptr) bool {
	return uint32(hr) == uint32(protocolActivationContractUnsupportedHRESULT)
}
~~~

- [ ] **Step 2: Add `activateApplication` using the existing COM manager**

The helper receives `context.Context`, `*applicationActivationManager`, `Detected`, and the raw URL. It validates the detector output, UTF-16 encodes `det.AppUserModelID` and the raw URL, calls the vtable's `ActivateApplication` member with zero options and a `uint32` process-ID out parameter, calls `runtime.KeepAlive` for both pointers, and returns `hresultError` on failure.

- [ ] **Step 3: Restrict fallback at the current failure boundary**

Replace the existing unconditional protocol activation failure return with:

~~~go
if hresultFailed(hr) {
	if protocolActivationNeedsAppActivationFallback(hr) {
		return activateApplication(ctx, manager, det, rawURL)
	}
	return hresultError("IApplicationActivationManager.ActivateForProtocol", hr)
}
~~~

Do not add a shell path and do not treat any other HRESULT as a fallback condition.

- [ ] **Step 4: Run focused GREEN verification**

Run `go test ./internal/codexdesktop -run '^(TestProtocolActivationContractFallbackIsRestricted|TestWindowsLaunchDirectlyActivatesDetectedAUMID)$' -count=1`.

Expected: PASS.

- [ ] **Step 5: Commit this focused change**

Run `git add internal/codexdesktop/launch_windows.go internal/codexdesktop/launch_test.go` and commit with message `fix: fall back to direct Codex app activation`.

### Task 3: Verify the package and Windows behavior

**Files:** No source changes expected.

- [ ] **Step 1: Run regression checks**

Run `go test -count=1 ./...`, `go test -count=1 -tags=e2e ./test/e2e/windows`, `go vet ./...`, and `git diff --check`.

Expected: every command exits zero.

- [ ] **Step 2: Rebuild the Windows installer**

Run `make cross-windows`.

Expected: `packaging/windows/Output/agentserver-app-0.1.8-setup.exe` rebuilds successfully.

- [ ] **Step 3: Run real Session 2 acceptance**

Install the rebuilt EXE silently in the Windows 11 acceptance session, call the installed launch path, and require: exit code 0; the exact `OpenAI.Codex` PFN; literal manifest `codex` declaration; and a post-activation current-user Session 2 process under the verified package install root.

- [ ] **Step 4: Leave temporary acceptance artifacts untracked**

Remove temporary task registrations, scripts, logs, and detached acceptance worktrees. Do not commit any of them.
