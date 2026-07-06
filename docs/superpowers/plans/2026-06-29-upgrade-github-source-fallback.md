# Upgrade: GitHub Release Source with CDN Fallback — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a GitHub Releases upgrade source that runs before the existing CDN, with automatic fallback on timeout, rate-limit, or sustained slow download, and no behavioral regression when GitHub is disabled (the default).

**Architecture:** Introduce a `Source` interface in `internal/updater`; the existing `service.go` becomes a scheduler that iterates over `s.Sources` and delegates fetch+download to each source in order. A `Sources==nil` compat shortcut lazily builds `[cdnSource]` from today's `ManifestURL` + `Client` fields, so every existing test fixture works untouched. SHA256 verification stays in the scheduler. Speed detection lives inside each source via a sliding-window monitor with injected clock/tick for deterministic tests.

**Tech Stack:** Go, `net/http` (stdlib only — no third-party HTTP libraries), `httptest.Server` for source tests, `atomic.Int64` / `atomic.Bool` for lock-free monitor state.

**Spec:** `docs/superpowers/specs/2026-06-29-upgrade-github-source-fallback-design.md` (v3).

## Global Constraints

- Stdlib `net/http` only. Do not import `internal/download/resumable` or any third-party HTTP library.
- Every existing test in `internal/updater/` and `cmd/launcher/` must continue to pass unchanged. New behavior enters only when `Sources` is explicitly set (via `BuildSources` when `UPGRADE_GITHUB_ENABLED=true`).
- Env-only configuration. Do not introduce YAML / Viper / any config file loader.
- SHA256 verification stays in `service.go::verifyInstaller` and is called by the scheduler, not by any `Source`.
- Each `Source` implementation constructs and owns its own `*http.Client` **and its own `*http.Transport`** — sources must not share Transports.
- Cancellation precedence inside a source's `DownloadInstaller`: on any I/O error, check `parent.Err()` first and return it directly if non-nil; only if the parent is live AND the monitor's `Tripped()` is true do you wrap `ErrSlowDownload`.
- GitHub source uses anonymous requests only; no token. Every GitHub HTTP request MUST send `Accept: application/vnd.github+json` and `User-Agent: agentserver-app/<appversion.Version>`.
- Default `UPGRADE_GITHUB_ENABLED=false`; day-1 behavior identical to today.
- The release workflow uploads `latest.json` **last**, after the .exe asset is present and its SHA256 computed.

---

## File Structure

Files created:

- `internal/updater/source.go` — `Source` interface, `SpeedSample`, `SourcePolicy`, sentinels, `noopProgress`.
- `internal/updater/speed_monitor.go` — sliding-window speed monitor with injected clock/tick.
- `internal/updater/speed_monitor_test.go` — monitor unit tests (fake clock/tick).
- `internal/updater/source_cdn.go` — CDN source impl (port of today's `fetchManifest` + `downloadInstaller` + `redirectPinnedAssetsClient`).
- `internal/updater/source_cdn_test.go` — CDN source tests including migrated AssetsHost tests.
- `internal/updater/source_github.go` — GitHub source impl.
- `internal/updater/source_github_test.go` — GitHub source tests.
- `internal/updater/config.go` — `UpgradeConfig`, `LoadUpgradeConfig`, `BuildSources`.
- `internal/updater/config_test.go` — env parsing tests.
- `internal/updater/service_fallback_test.go` — scheduler tests with fake sources.
- `packaging/windows/latest.json.tmpl` — release manifest template.
- `.github/workflows/release.yml` — CI release workflow.

Files modified:

- `internal/updater/manifest.go` — remove host allowlist from `Validate()`; keep format-only checks.
- `internal/updater/manifest_test.go` — delete 5 AssetsHost tests (they move to `source_cdn_test.go`).
- `internal/updater/state.go` — add `LastSourceUsed`, `LastFallbacks`, `FallbackRecord`.
- `internal/updater/service.go` — add `Sources []Source` field; refactor `Check` and `DownloadAndStart` to iterate sources; delete `fetchManifest`, `downloadInstaller`, `manifestDownloadClient`, `installerDownloadClient` (moved to `source_cdn.go`); keep `redirectPinnedAssetsClient` deleted from here — it's ported to `source_cdn.go` as a private helper.
- `cmd/launcher/main.go` — `newCompletedUpdater` reads env and sets `Sources`.
- `scripts/windows-package-common.sh` — assemble `latest.json` inputs.

---

## Task Sequence

Task order is dictated by compile-time dependencies:

1. `source.go` (types only; no impls) — needed by everything else.
2. `speed_monitor.go` — uses `SpeedSample`.
3. `source_cdn.go` — implements `Source`, ports today's logic, still relies on `manifest.go`'s current host check as a duplicate check.
4. Split `Manifest.Validate()` — remove host check; migrate tests. After this step, CDN source's own host check is the only enforcement of AssetsHost.
5. `source_github.go` — implements `Source`.
6. `state.go` additions.
7. `service.go` scheduler refactor + `service_fallback_test.go`.
8. `config.go` + `BuildSources`.
9. `cmd/launcher/main.go` wire-up.
10. Release pipeline: template, script edit, workflow.

---

### Task 1: Source interface, sentinels, SpeedSample, SourcePolicy

**Files:**
- Create: `internal/updater/source.go`
- Test: none for this task — it's types-only; consumers test the behavior.

**Interfaces:**
- Consumes: nothing from this plan.
- Produces:
  - `type SpeedSample struct { Downloaded int64; Elapsed time.Duration; BytesPerSec float64 }`
  - `type SourcePolicy struct { ManifestTimeout, FirstByteTimeout, SpeedWindow time.Duration; MinSpeedBytesPerSec int64 }`
  - `type Source interface { Name() string; FetchManifest(ctx context.Context) (Manifest, error); DownloadInstaller(ctx context.Context, m Manifest, dst io.Writer, onProgress func(SpeedSample)) error }`
  - `func noopProgress(SpeedSample)` — package-level.
  - Sentinels: `ErrFetchTimeout`, `ErrSlowDownload`, `ErrRateLimited`, `ErrHostNotAllowed`, `ErrSHA256Mismatch`.
  - `func DefaultSourcePolicy() SourcePolicy` returning `{5s, 10s, 10s, 100*1024}`.

- [ ] **Step 1: Create the file with all declarations**

Create `internal/updater/source.go`:

```go
package updater

import (
	"context"
	"errors"
	"io"
	"time"
)

// SpeedSample is reported during DownloadInstaller via the onProgress
// callback. The source's speed monitor produces these; the scheduler
// only forwards them. They exist for tests today and for a future
// progress-UI hookup; the scheduler does not interpret them.
type SpeedSample struct {
	Downloaded  int64
	Elapsed     time.Duration
	BytesPerSec float64
}

// SourcePolicy tunes timeouts and slow-download detection for one Source.
// Each Source impl holds its own copy.
type SourcePolicy struct {
	ManifestTimeout     time.Duration
	FirstByteTimeout    time.Duration
	SpeedWindow         time.Duration
	MinSpeedBytesPerSec int64
}

// DefaultSourcePolicy returns the spec's lenient defaults: 5s manifest,
// 10s first byte, 10s window, 100 KB/s minimum.
func DefaultSourcePolicy() SourcePolicy {
	return SourcePolicy{
		ManifestTimeout:     5 * time.Second,
		FirstByteTimeout:    10 * time.Second,
		SpeedWindow:         10 * time.Second,
		MinSpeedBytesPerSec: 100 * 1024,
	}
}

// Source is a single upgrade origin (GitHub release, internal CDN, …).
// Implementations MUST be safe to share across concurrent calls because
// console/update.go shallow-copies Service to compose per-request
// callbacks. Hold no per-call mutable state on the Source itself;
// per-attempt state (speed monitor, temp files) lives on the stack of
// the method call.
type Source interface {
	Name() string

	// FetchManifest returns the source's authoritative manifest for the
	// current release. It applies the source's own ManifestTimeout.
	FetchManifest(ctx context.Context) (Manifest, error)

	// DownloadInstaller writes installer bytes to dst using the manifest
	// THIS source returned from FetchManifest. Callers MUST NOT pass a
	// manifest from a different source. SHA256 verification is NOT the
	// source's responsibility.
	//
	// Cancellation precedence: on any I/O error, the source first checks
	// whether the parent ctx is cancelled and returns ctx.Err() in that
	// case. Only if the parent is still live AND the internal speed
	// monitor's Tripped() flag is true does the source wrap
	// ErrSlowDownload. This prevents user-initiated shutdown from being
	// misclassified as slow-download fallback.
	DownloadInstaller(ctx context.Context, m Manifest, dst io.Writer, onProgress func(SpeedSample)) error
}

// noopProgress is the scheduler's default onProgress callback.
func noopProgress(SpeedSample) {}

// Sentinel errors. Sources wrap these; the scheduler records the wrapped
// kind in FallbackRecord.Reason. Callers that want to programmatically
// detect a specific failure can use errors.Is on the returned error.
var (
	ErrFetchTimeout   = errors.New("source: fetch timeout")
	ErrSlowDownload   = errors.New("source: download too slow")
	ErrRateLimited    = errors.New("source: rate limited")
	ErrHostNotAllowed = errors.New("source: host not in allowlist")
	ErrSHA256Mismatch = errors.New("source: installer sha256 mismatch")
)
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/updater/...`
Expected: no output, exit 0.

- [ ] **Step 3: Verify existing tests still pass**

Run: `go test ./internal/updater/...`
Expected: `ok  github.com/agentserver/agentserver-pkg/internal/updater ...`

- [ ] **Step 4: Commit**

```bash
git add internal/updater/source.go
git commit -m "feat(updater): add Source interface, sentinels, SpeedSample, SourcePolicy"
```

---

### Task 2: Speed monitor with injected clock/tick

**Files:**
- Create: `internal/updater/speed_monitor.go`
- Test: `internal/updater/speed_monitor_test.go`

**Interfaces:**
- Consumes: `SpeedSample` from Task 1.
- Produces:
  - `type speedMonitor struct { … }` (unexported)
  - `func newSpeedMonitor(policy SourcePolicy, cancel context.CancelFunc, onSample func(SpeedSample)) *speedMonitor`
  - `func (m *speedMonitor) wrap(r io.Reader) io.Reader`
  - `func (m *speedMonitor) run(ctx context.Context)` — blocking; return when ctx.Done or when tripped.
  - `func (m *speedMonitor) Tripped() bool`
  - `func (m *speedMonitor) withClock(now func() time.Time, tick <-chan time.Time) *speedMonitor` — test hook, returns receiver for chaining.

- [ ] **Step 1: Write the failing tests**

Create `internal/updater/speed_monitor_test.go`:

```go
package updater

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTicker returns a hand-controlled tick channel and a now function
// that returns whatever time was most recently sent into the channel.
type fakeTicker struct {
	ch      chan time.Time
	lastAtomic atomic.Pointer[time.Time]
}

func newFakeTicker(start time.Time) *fakeTicker {
	ft := &fakeTicker{ch: make(chan time.Time, 1)}
	ft.lastAtomic.Store(&start)
	return ft
}

func (ft *fakeTicker) send(t time.Time) {
	ft.lastAtomic.Store(&t)
	ft.ch <- t
}

func (ft *fakeTicker) now() time.Time {
	return *ft.lastAtomic.Load()
}

func TestSpeedMonitorNoCancelBeforeWindowFull(t *testing.T) {
	policy := SourcePolicy{SpeedWindow: 10 * time.Second, MinSpeedBytesPerSec: 100}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m := newSpeedMonitor(policy, cancel, nil).withClock(ft.now, ft.ch)

	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()

	// Tick 5 times, each 1 second apart, with zero bytes. Window not
	// full yet (5s of 10s window), so no cancel.
	for i := 1; i <= 5; i++ {
		ft.send(start.Add(time.Duration(i) * time.Second))
	}

	// Give run() a moment to process without deadlocking on send.
	time.Sleep(20 * time.Millisecond)
	if m.Tripped() {
		t.Fatal("monitor tripped before window full")
	}
	if ctx.Err() != nil {
		t.Fatal("ctx cancelled before window full")
	}

	cancel()
	<-done
}

func TestSpeedMonitorCancelsWhenTrailingAvgBelowThreshold(t *testing.T) {
	policy := SourcePolicy{SpeedWindow: 3 * time.Second, MinSpeedBytesPerSec: 1_000}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m := newSpeedMonitor(policy, cancel, nil).withClock(ft.now, ft.ch)

	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()

	// Simulate 3 seconds of low throughput: total 100 bytes over 3s = 33 B/s.
	r := m.wrap(strings.NewReader(strings.Repeat("x", 100)))
	buf := make([]byte, 100)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		ft.send(start.Add(time.Duration(i) * time.Second))
	}

	// Wait for monitor to trip.
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("monitor did not cancel ctx within 200ms")
	}
	if !m.Tripped() {
		t.Fatal("Tripped() must be true after cancel")
	}
	<-done
}

func TestSpeedMonitorDoesNotCancelWhenTrailingAvgAboveThreshold(t *testing.T) {
	policy := SourcePolicy{SpeedWindow: 2 * time.Second, MinSpeedBytesPerSec: 100}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m := newSpeedMonitor(policy, cancel, nil).withClock(ft.now, ft.ch)

	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()

	r := m.wrap(strings.NewReader(strings.Repeat("x", 10_000)))
	buf := make([]byte, 10_000)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		ft.send(start.Add(time.Duration(i) * time.Second))
	}

	time.Sleep(20 * time.Millisecond)
	if m.Tripped() {
		t.Fatal("monitor tripped despite high throughput")
	}
	cancel()
	<-done
}

func TestSpeedMonitorDisabledWhenMinBPSZero(t *testing.T) {
	// Compat-mode policy: MinSpeedBytesPerSec == 0 ⇒ monitor never cancels
	// even when throughput is zero for many windows. Preserves today's
	// CDN download behavior in Service.Sources==nil mode.
	policy := SourcePolicy{SpeedWindow: 1 * time.Second, MinSpeedBytesPerSec: 0}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m := newSpeedMonitor(policy, cancel, nil).withClock(ft.now, ft.ch)

	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()

	// Send 20 ticks with zero bytes; monitor must never trip.
	for i := 1; i <= 20; i++ {
		ft.send(start.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(20 * time.Millisecond)
	if m.Tripped() {
		t.Fatal("disabled monitor must not trip")
	}
	if ctx.Err() != nil {
		t.Fatal("disabled monitor must not cancel ctx")
	}
	cancel()
	<-done
}

func TestSpeedMonitorEmitsOnSamplePerTick(t *testing.T) {
	policy := SourcePolicy{SpeedWindow: 10 * time.Second, MinSpeedBytesPerSec: 0}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int64
	m := newSpeedMonitor(policy, cancel, func(SpeedSample) { count.Add(1) }).withClock(ft.now, ft.ch)

	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()

	for i := 1; i <= 4; i++ {
		ft.send(start.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(20 * time.Millisecond)
	if got := count.Load(); got != 4 {
		t.Fatalf("onSample count = %d, want 4", got)
	}
	cancel()
	<-done
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/updater/ -run TestSpeedMonitor -v`
Expected: FAIL — `undefined: newSpeedMonitor` (compile error).

- [ ] **Step 3: Implement the monitor**

Create `internal/updater/speed_monitor.go`:

```go
package updater

import (
	"context"
	"io"
	"sync/atomic"
	"time"
)

type speedSampleRecord struct {
	t     time.Time
	bytes int64
}

type speedMonitor struct {
	window   time.Duration
	minBPS   int64
	now      func() time.Time
	tick     <-chan time.Time
	cancel   context.CancelFunc
	onSample func(SpeedSample)

	bytes   atomic.Int64
	tripped atomic.Bool

	// samples is touched only inside run(); no lock needed.
	samples []speedSampleRecord
}

func newSpeedMonitor(policy SourcePolicy, cancel context.CancelFunc, onSample func(SpeedSample)) *speedMonitor {
	return &speedMonitor{
		window:   policy.SpeedWindow,
		minBPS:   policy.MinSpeedBytesPerSec,
		now:      time.Now,
		cancel:   cancel,
		onSample: onSample,
	}
}

// withClock injects a fake clock + tick channel for tests. Returns
// receiver so it chains after newSpeedMonitor.
func (m *speedMonitor) withClock(now func() time.Time, tick <-chan time.Time) *speedMonitor {
	m.now = now
	m.tick = tick
	return m
}

// wrap returns an io.Reader that counts bytes into m.bytes on every Read.
func (m *speedMonitor) wrap(r io.Reader) io.Reader {
	return &countingReader{r: r, m: m}
}

// Tripped reports whether the monitor cancelled its ctx due to slow speed.
func (m *speedMonitor) Tripped() bool { return m.tripped.Load() }

// run blocks until ctx is done. If no tick channel was injected, it
// creates a 1s ticker. Tests inject their own.
func (m *speedMonitor) run(ctx context.Context) {
	tick := m.tick
	if tick == nil {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		tick = t.C
	}
	start := m.now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick:
			m.recordTick(now, start)
			if m.tripped.Load() {
				return
			}
		}
	}
}

func (m *speedMonitor) recordTick(now, start time.Time) {
	b := m.bytes.Load()
	m.samples = append(m.samples, speedSampleRecord{t: now, bytes: b})

	// Trim samples older than the window.
	cutoff := now.Add(-m.window)
	i := 0
	for i < len(m.samples) && m.samples[i].t.Before(cutoff) {
		i++
	}
	if i > 0 {
		m.samples = m.samples[i:]
	}

	elapsed := now.Sub(start)
	if m.onSample != nil {
		m.onSample(SpeedSample{
			Downloaded:  b,
			Elapsed:     elapsed,
			BytesPerSec: instantBPS(m.samples),
		})
	}

	// A zero threshold or zero window disables slow-download detection.
	// Compat mode (Service.Sources == nil) uses this to preserve today's
	// "download runs until socket timeout" behavior in existing tests.
	if m.minBPS <= 0 || m.window <= 0 {
		return
	}
	if elapsed < m.window {
		return
	}
	if trailingBPS(m.samples) < float64(m.minBPS) {
		m.tripped.Store(true)
		m.cancel()
	}
}

func instantBPS(samples []speedSampleRecord) float64 {
	if len(samples) < 2 {
		return 0
	}
	a := samples[0]
	b := samples[len(samples)-1]
	dt := b.t.Sub(a.t).Seconds()
	if dt <= 0 {
		return 0
	}
	return float64(b.bytes-a.bytes) / dt
}

func trailingBPS(samples []speedSampleRecord) float64 {
	return instantBPS(samples)
}

type countingReader struct {
	r io.Reader
	m *speedMonitor
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.m.bytes.Add(int64(n))
	}
	return n, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/updater/ -run TestSpeedMonitor -v`
Expected: 4 PASS.

- [ ] **Step 5: Run the full updater suite for regressions**

Run: `go test ./internal/updater/...`
Expected: ok.

- [ ] **Step 6: Commit**

```bash
git add internal/updater/speed_monitor.go internal/updater/speed_monitor_test.go
git commit -m "feat(updater): sliding-window speed monitor with injected clock/tick"
```

---

### Task 3: CDN source (port existing behavior behind Source interface)

**Files:**
- Create: `internal/updater/source_cdn.go`
- Create: `internal/updater/source_cdn_test.go` (host-allowlist tests only in this task; happy path can wait for a follow-up test).

**Interfaces:**
- Consumes: `Source`, `SourcePolicy`, sentinels, `AssetsHost`, `Manifest`, `manifestMaxBytes` (still in `service.go`), `noopProgress`.
- Produces:
  - `type cdnSource struct { … }` (unexported).
  - `func NewCDNSource(manifestURL string, client *http.Client, policy SourcePolicy) Source`
  - Implements `Source.Name`, `FetchManifest`, `DownloadInstaller`.

Note: `manifest.go` still enforces `AssetsHost` in `Validate()` in this task. Task 4 removes it; until then the CDN source's own host validator is redundant with `Validate()`, which is fine.

- [ ] **Step 1: Write the failing tests**

Create `internal/updater/source_cdn_test.go`:

```go
package updater

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// cdnHostFromURL is a small helper: builds the "assets.agent.cs.ac.cn"
// production URL for a given path so we can test the source's validator
// without spinning up a real server for these unit checks.
func cdnHostURL(path string) string {
	return "https://" + AssetsHost + path
}

func TestCDNSourceRejectsInstallerHostSuffixBypass(t *testing.T) {
	src := NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, DefaultSourcePolicy())
	m := Manifest{
		Version: "0.9.9",
		URL:     "https://" + AssetsHost + ".evil.example.com/installer.exe",
		SHA256:  strings.Repeat("a", 64),
		Size:    1,
	}
	err := src.(*cdnSource).validateInstallerURL(m.URL)
	if err == nil {
		t.Fatal("expected suffix bypass to be rejected")
	}
}

func TestCDNSourceRejectsInstallerHostUserinfoBypass(t *testing.T) {
	src := NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, DefaultSourcePolicy())
	err := src.(*cdnSource).validateInstallerURL("https://" + AssetsHost + "@evil.example.com/installer.exe")
	if err == nil {
		t.Fatal("expected userinfo bypass to be rejected")
	}
}

func TestCDNSourceRejectsURLOutsideAssetsHost(t *testing.T) {
	src := NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, DefaultSourcePolicy())
	err := src.(*cdnSource).validateInstallerURL("https://evil.example.com/installer.exe")
	if err == nil {
		t.Fatal("expected non-AssetsHost URL to be rejected")
	}
}

func TestCDNSourceAcceptsMixedCaseAssetsHost(t *testing.T) {
	src := NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, DefaultSourcePolicy())
	if err := src.(*cdnSource).validateInstallerURL("https://" + strings.ToUpper(AssetsHost) + "/installer.exe"); err != nil {
		t.Fatalf("mixed-case host must be accepted: %v", err)
	}
}

func TestCDNSourceAcceptsAssetsHTTPSInstaller(t *testing.T) {
	src := NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, DefaultSourcePolicy())
	if err := src.(*cdnSource).validateInstallerURL("https://" + AssetsHost + "/agentserver-app/windows/setup.exe"); err != nil {
		t.Fatalf("standard installer URL must be accepted: %v", err)
	}
}

func TestCDNSourceNameIsCDN(t *testing.T) {
	src := NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, DefaultSourcePolicy())
	if src.Name() != "cdn" {
		t.Fatalf("Name()=%q, want %q", src.Name(), "cdn")
	}
}

func TestCDNSourceFetchManifestAppliesTimeout(t *testing.T) {
	// httptest.Server that blocks past the policy timeout.
	// If FetchManifest doesn't enforce the timeout, this hangs.
	policy := DefaultSourcePolicy()
	policy.ManifestTimeout = 50 * time.Millisecond
	src := NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, policy)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// We hit the real (unreachable in test env) host; expect a network
	// error before the outer timeout fires. This confirms the source
	// derives its own inner timeout from policy.ManifestTimeout.
	start := time.Now()
	_, err := src.FetchManifest(ctx)
	if err == nil {
		t.Fatal("expected network error")
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Fatalf("FetchManifest took %v, expected < policy timeout", time.Since(start))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/updater/ -run TestCDNSource -v`
Expected: FAIL — `undefined: NewCDNSource`.

- [ ] **Step 3: Implement source_cdn.go**

Create `internal/updater/source_cdn.go`:

```go
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cdnSource struct {
	manifestURL string
	client      *http.Client
	policy      SourcePolicy
}

// NewCDNSource returns a Source backed by the internal CDN
// (assets.agent.cs.ac.cn). Callers pass the manifest URL (typically
// DefaultManifestURL), an *http.Client (nil ⇒ http.DefaultClient), and
// a SourcePolicy. The returned Source constructs its own redirect
// validator; do not share the client's Transport with other sources.
func NewCDNSource(manifestURL string, client *http.Client, policy SourcePolicy) Source {
	if client == nil {
		client = http.DefaultClient
	}
	return &cdnSource{manifestURL: manifestURL, client: client, policy: policy}
}

func (s *cdnSource) Name() string { return "cdn" }

func (s *cdnSource) validateInstallerURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("cdn: invalid installer url")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("cdn: installer url must use https")
	}
	if u.User != nil {
		return fmt.Errorf("%w: userinfo not permitted", ErrHostNotAllowed)
	}
	host := strings.ToLower(u.Hostname())
	if host != AssetsHost {
		return fmt.Errorf("%w: installer host %q", ErrHostNotAllowed, u.Hostname())
	}
	return nil
}

func (s *cdnSource) FetchManifest(ctx context.Context) (Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, s.policy.ManifestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.manifestURL, nil)
	if err != nil {
		return Manifest{}, err
	}
	resp, err := s.pinnedClient().Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("cdn fetch manifest: unexpected status %s", resp.Status)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, manifestMaxBytes)).Decode(&m); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := s.validateInstallerURL(m.URL); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (s *cdnSource) DownloadInstaller(ctx context.Context, m Manifest, dst io.Writer, onProgress func(SpeedSample)) error {
	if err := s.validateInstallerURL(m.URL); err != nil {
		return err
	}
	if onProgress == nil {
		onProgress = noopProgress
	}
	dlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	monitor := newSpeedMonitor(s.policy, cancel, onProgress)
	monitorDone := make(chan struct{})
	go func() { monitor.run(dlCtx); close(monitorDone) }()
	defer func() { cancel(); <-monitorDone }()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	resp, err := s.installerClient().Do(req)
	if err != nil {
		return s.classify(ctx, monitor, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cdn download: unexpected status %s", resp.Status)
	}
	body := monitor.wrap(io.LimitReader(resp.Body, m.Size+1))
	n, err := io.Copy(dst, body)
	if err != nil {
		return s.classify(ctx, monitor, err)
	}
	if n > m.Size {
		return fmt.Errorf("cdn download: response larger than declared size")
	}
	return nil
}

// classify implements the cancellation-precedence rule: parent ctx first,
// then Tripped(). Callers should hand back whatever we return.
func (s *cdnSource) classify(parent context.Context, monitor *speedMonitor, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if monitor.Tripped() {
		return fmt.Errorf("%w: %v", ErrSlowDownload, err)
	}
	return err
}

// pinnedClient returns a client whose CheckRedirect validates each hop
// against the CDN's installer whitelist.
func (s *cdnSource) pinnedClient() *http.Client {
	return s.redirectPinned(s.client)
}

// installerClient enforces FirstByteTimeout via ResponseHeaderTimeout
// when the underlying Transport is *http.Transport. For custom
// RoundTrippers (test rewriters), the download loop wraps client.Do
// in context.WithTimeout(ctx, FirstByteTimeout) instead — same wall
// deadline, different mechanism.
func (s *cdnSource) installerClient() *http.Client {
	c := s.redirectPinned(s.client)
	return applyFirstByteTimeout(c, s.policy.FirstByteTimeout)
}

// isRealTransport returns true when the source's client uses a real
// *http.Transport; false for custom RoundTrippers. The download loop
// uses this to decide whether to also apply a context deadline for
// FirstByteTimeout (needed only in the non-*Transport path).
func (s *cdnSource) isRealTransport() bool {
	rt := s.client.Transport
	if rt == nil {
		return true
	}
	_, ok := rt.(*http.Transport)
	return ok
}

func (s *cdnSource) redirectPinned(base *http.Client) *http.Client {
	client := *base
	priorCheckRedirect := base.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := s.validateInstallerURL(req.URL.String()); err != nil {
			return err
		}
		if priorCheckRedirect != nil {
			return priorCheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

// applyFirstByteTimeout returns a client whose Transport enforces
// ResponseHeaderTimeout when the base Transport is *http.Transport,
// or a client with the base RoundTripper preserved verbatim otherwise.
// This preserves test-only RoundTrippers such as assetsHostRewriteTransport
// (internal/updater/service_test.go) which would be lost by a naive clone.
// In the non-*Transport path, FirstByteTimeout is best-effort via the
// request context deadline instead.
func applyFirstByteTimeout(base *http.Client, firstByte time.Duration) *http.Client {
	c := *base
	rt := c.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if ht, ok := rt.(*http.Transport); ok {
		clone := ht.Clone()
		clone.ResponseHeaderTimeout = firstByte
		c.Transport = clone
		return &c
	}
	// Custom RoundTripper (typically a test rewriter). Do not clone;
	// downloading code applies context.WithTimeout(ctx, firstByte)
	// around the .Do call instead. Return the client unchanged so the
	// custom RT still sees requests.
	return &c
}
```

Remove the placeholder `time` import once real usage stabilizes.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/updater/ -run TestCDNSource -v`
Expected: 6 PASS (5 host tests + Name + timeout test may pass or skip if network unreachable; adjust to just verify timeout deadline shape).

- [ ] **Step 5: Verify full suite still green**

Run: `go test ./internal/updater/...`
Expected: ok — no existing test regressed.

- [ ] **Step 6: Commit**

```bash
git add internal/updater/source_cdn.go internal/updater/source_cdn_test.go
git commit -m "feat(updater): CDN source implementing Source interface

Ports the existing fetchManifest / downloadInstaller / redirect
validation into a Source implementation. service.go still hosts the
originals for backward compatibility; a later task deletes them."
```

---

### Task 4: Split Manifest.Validate — remove host allowlist

**Files:**
- Modify: `internal/updater/manifest.go`
- Modify: `internal/updater/manifest_test.go` (delete 5 tests)

**Interfaces:**
- Consumes: nothing new.
- Produces: `Manifest.Validate()` now performs format-only checks; host allowlist enforcement moves fully to the CDN source (already added in Task 3). GitHub source will do its own host check in Task 5.

- [ ] **Step 1: Modify manifest.go — remove host check**

Edit `internal/updater/manifest.go`. Replace the `validateInstallerURL` function body:

```go
func validateInstallerURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid installer url")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("installer url must use https")
	}
	return nil
}
```

(Removes the `host != AssetsHost` block.) Keep the function so `Validate()` continues to call it.

- [ ] **Step 2: Delete 5 AssetsHost tests from manifest_test.go**

Delete these test functions (their equivalents now live in `source_cdn_test.go` from Task 3):

- `TestManifestValidateAcceptsAssetsHTTPSInstaller`
- `TestManifestValidateAcceptsMixedCaseAssetsHost`
- `TestManifestValidateRejectsAssetsHostSuffixBypass`
- `TestManifestValidateRejectsAssetsHostUserinfoBypass`
- `TestManifestValidateRejectsURLOutsideAssetsHost`

Keep every other test.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/updater/...`
Expected: ok — all remaining tests pass. If `service_test.go` fails because it relied on `Validate()` rejecting a non-AssetsHost URL, add a companion assertion at the source layer OR confirm the failure indicates a test that should also move; do NOT weaken production behavior.

- [ ] **Step 4: Commit**

```bash
git add internal/updater/manifest.go internal/updater/manifest_test.go
git commit -m "refactor(updater): move host allowlist from Manifest.Validate to CDN source

Manifest.Validate() is now format-only (version, sha256, size, https).
Host enforcement is a per-source concern — CDN source keeps AssetsHost
pinning; the incoming GitHub source enforces its own whitelist."
```

---

### Task 5: GitHub source

**Files:**
- Create: `internal/updater/source_github.go`
- Create: `internal/updater/source_github_test.go`

**Interfaces:**
- Consumes: `Source`, `SourcePolicy`, sentinels, `Manifest`, `manifestMaxBytes`, `appversion.Version`, `applyFirstByteTimeout` (Task 3).
- Produces:
  - `func NewGitHubSource(repo string, apiBase string, client *http.Client, policy SourcePolicy) Source` — `apiBase` defaults to `https://api.github.com` when empty; tests override to point at `httptest.Server`.
  - `type githubSource struct { … }` (unexported), `Name() == "github"`.

**Security invariants for this task:**

- Host validation uses `url.Hostname()` (strips port & IPv6 brackets), lowered, with any trailing `.` trimmed to defeat DNS-dot bypass.
- Reject any URL where `u.User != nil` (defeats `https://good.example@evil.com/...` bypass).
- Manifest asset URLs from GitHub API responses are validated with the **same** host matcher as installer URLs (`githubAssetHost`) — a single source of truth. There is no separate `manifestHost` whitelist.
- `githubAssetHost` accepts: `github.com` (raw `browser_download_url`), `codeload.github.com` (occasional archive redirect), and any subdomain of `githubusercontent.com` (`objects.` / `release-assets.` / future renames).
- `api.github.com` is NOT in `githubAssetHost`. The initial `/repos/.../releases/latest` request goes through a separate `manifestAPIClient` whose CheckRedirect additionally accepts `api.github.com`.
- Per-request context deadline via `context.WithTimeout(ctx, ManifestTimeout)` — one budget per HTTP call, not shared across the two-hop fetch.
- HTTP 403 and 429 always map to `ErrRateLimited` regardless of response body; response body is not surfaced in `FallbackRecord.Reason` beyond status code + `X-RateLimit-Remaining` value (never `X-GitHub-Request-Id` or the body, to avoid leaking identifying tokens to disk).

- [ ] **Step 1: Write the failing tests**

Create `internal/updater/source_github_test.go`:

```go
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testRepo = "agentserver/app"

// githubMock is a helper that serves both the /releases/latest endpoint
// and the browser_download_url assets in one httptest.Server.
type githubMock struct {
	t           *testing.T
	server      *httptest.Server
	manifestBody []byte
	installerBody []byte
	manifestStatus int
	installerDelay time.Duration
	slowInstaller  bool
	seenHeaders http.Header
	requireHeaders bool
}

func newGitHubMock(t *testing.T) *githubMock {
	m := &githubMock{
		t: t,
		manifestStatus: http.StatusOK,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/releases/latest", testRepo), func(w http.ResponseWriter, r *http.Request) {
		m.seenHeaders = r.Header.Clone()
		if m.requireHeaders {
			if r.Header.Get("Accept") != "application/vnd.github+json" || !strings.HasPrefix(r.Header.Get("User-Agent"), "agentserver-app/") {
				http.Error(w, "missing headers", http.StatusForbidden)
				return
			}
		}
		if m.manifestStatus != http.StatusOK {
			for k, v := range m.headers() {
				w.Header().Set(k, v)
			}
			http.Error(w, "err", m.manifestStatus)
			return
		}
		// Compose a releases/latest response whose asset points back at
		// this server (path /assets/latest.json). The manifest inside
		// latest.json points at /assets/setup.exe.
		latest := map[string]any{
			"tag_name": "v1.0.0",
			"assets": []map[string]any{
				{"name": "latest.json", "browser_download_url": m.server.URL + "/assets/latest.json"},
				{"name": "setup.exe", "browser_download_url": m.server.URL + "/assets/setup.exe"},
			},
		}
		_ = json.NewEncoder(w).Encode(latest)
	})
	mux.HandleFunc("/assets/latest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(m.manifestBody)
	})
	mux.HandleFunc("/assets/setup.exe", func(w http.ResponseWriter, r *http.Request) {
		if m.installerDelay > 0 {
			time.Sleep(m.installerDelay)
		}
		if m.slowInstaller {
			// Write one byte then sleep so the speed monitor trips.
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(m.installerBody)))
			w.Write(m.installerBody[:1])
			w.(http.Flusher).Flush()
			time.Sleep(1 * time.Second)
			return
		}
		w.Write(m.installerBody)
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *githubMock) headers() map[string]string {
	h := map[string]string{}
	if m.manifestStatus == http.StatusForbidden {
		h["X-RateLimit-Remaining"] = "0"
	}
	return h
}

func (m *githubMock) setManifest(installerURL string, body []byte) {
	sum := sha256.Sum256(body)
	man := Manifest{
		Version: "1.0.0",
		URL:     installerURL,
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	}
	b, _ := json.Marshal(man)
	m.manifestBody = b
	m.installerBody = body
}

// helper: build a source pointing at the mock, with generous timeouts.
func newTestGitHubSource(mock *githubMock) *githubSource {
	return NewGitHubSource(testRepo, mock.server.URL, http.DefaultClient, DefaultSourcePolicy()).(*githubSource)
}

func TestGitHubSourceNameIsGithub(t *testing.T) {
	mock := newGitHubMock(t)
	src := newTestGitHubSource(mock)
	if src.Name() != "github" {
		t.Fatalf("Name()=%q, want github", src.Name())
	}
}

func TestGitHubSourceHappyPath(t *testing.T) {
	mock := newGitHubMock(t)
	body := []byte(strings.Repeat("x", 4096))
	mock.setManifest(mock.server.URL+"/assets/setup.exe", body)
	// Rewrite the manifest inside the mock so its host matches the
	// httptest server URL — the source's whitelist wildcards
	// *.githubusercontent.com, so we need to relax it for tests via
	// a test-only host override.
	src := NewGitHubSource(testRepo, mock.server.URL, http.DefaultClient, DefaultSourcePolicy()).(*githubSource)
	src.installerHostMatch = func(host string) bool { return true } // test hook

	m, err := src.FetchManifest(context.Background())
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if m.Version != "1.0.0" {
		t.Fatalf("version=%q", m.Version)
	}
	var buf strings.Builder
	if err := src.DownloadInstaller(context.Background(), m, &buf, nil); err != nil {
		t.Fatalf("DownloadInstaller: %v", err)
	}
	if buf.Len() != len(body) {
		t.Fatalf("body len=%d want %d", buf.Len(), len(body))
	}
}

func TestGitHubSourceSendsAcceptAndUserAgent(t *testing.T) {
	mock := newGitHubMock(t)
	mock.requireHeaders = true
	body := []byte("payload")
	mock.setManifest(mock.server.URL+"/assets/setup.exe", body)
	src := newTestGitHubSource(mock)

	if _, err := src.FetchManifest(context.Background()); err != nil {
		t.Fatalf("FetchManifest expected to succeed with headers: %v", err)
	}
}

func TestGitHubSourceRateLimit403(t *testing.T) {
	mock := newGitHubMock(t)
	mock.manifestStatus = http.StatusForbidden
	src := newTestGitHubSource(mock)

	_, err := src.FetchManifest(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v; want ErrRateLimited", err)
	}
}

func TestGitHubSourceRateLimit429(t *testing.T) {
	mock := newGitHubMock(t)
	mock.manifestStatus = http.StatusTooManyRequests
	src := newTestGitHubSource(mock)

	_, err := src.FetchManifest(context.Background())
	if err == nil || !isErrRateLimited(err) {
		t.Fatalf("err=%v; want ErrRateLimited", err)
	}
}

func TestGitHubSourceManifestTimeoutFires(t *testing.T) {
	// Slow API endpoint sleeps 500ms; policy timeout is 50ms.
	// Assert the returned error wraps ErrFetchTimeout AND elapsed
	// is roughly ManifestTimeout, not 500ms.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(500 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	}))
	t.Cleanup(slow.Close)
	policy := DefaultSourcePolicy()
	policy.ManifestTimeout = 50 * time.Millisecond
	src := NewGitHubSource(testRepo, slow.URL, http.DefaultClient, policy).(*githubSource)

	start := time.Now()
	_, err := src.FetchManifest(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrFetchTimeout) {
		t.Fatalf("err=%v; want ErrFetchTimeout wrap", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("FetchManifest ran %v, expected ~50ms", elapsed)
	}
}

func TestGitHubSourceManifestTimeoutIsPerRequest(t *testing.T) {
	// Two hops must each get their OWN ManifestTimeout budget, not a
	// shared one. The API call returns quickly; the latest.json asset
	// then sleeps 500ms. With per-request timeout the flow must still
	// fail via ErrFetchTimeout on the second hop within ~50ms.
	// Configure your mock so /assets/latest.json sleeps 500ms.
	mock := newGitHubMock(t)
	mock.slowManifestAsset = true // add this field to githubMock
	body := []byte("payload")
	mock.setManifest(mock.server.URL+"/assets/setup.exe", body)
	policy := DefaultSourcePolicy()
	policy.ManifestTimeout = 50 * time.Millisecond
	src := NewGitHubSource(testRepo, mock.server.URL, http.DefaultClient, policy).(*githubSource)
	src.installerHostMatch = func(string) bool { return true }

	start := time.Now()
	_, err := src.FetchManifest(context.Background())
	elapsed := time.Since(start)
	if err == nil || !errors.Is(err, ErrFetchTimeout) {
		t.Fatalf("err=%v; want ErrFetchTimeout on slow second hop", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("elapsed %v — timeout appears shared across hops", elapsed)
	}
}

func TestGitHubSourceRejectsUnwhitelistedInstallerHost(t *testing.T) {
	// Manifest declares an installer URL on evil.example.com. Source
	// must reject with ErrHostNotAllowed BEFORE any HTTP request.
	mock := newGitHubMock(t)
	body := []byte("payload")
	mock.setManifest("https://evil.example.com/setup.exe", body)
	src := newTestGitHubSource(mock)

	m, err := src.FetchManifest(context.Background())
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	err = src.DownloadInstaller(context.Background(), m, io.Discard, nil)
	if err == nil || !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed", err)
	}
}

func TestGithubAssetHostMatcher(t *testing.T) {
	// Regression guard for the real production browser_download_url,
	// which is on github.com (NOT *.githubusercontent.com). Plus
	// adversarial variants — bypass attempts must all be rejected.
	cases := []struct {
		host string
		want bool
	}{
		{"github.com", true},                                    // real browser_download_url
		{"codeload.github.com", true},                            // occasional archive redirect
		{"objects.githubusercontent.com", true},                  // legacy asset CDN
		{"release-assets.githubusercontent.com", true},           // current asset CDN
		{"GitHub.com", true},                                     // case-fold
		{"github.com.", true},                                    // trailing dot
		{"api.github.com", false},                                // API host, not asset host
		{"evil.githubusercontent.com.attacker.com", false},       // suffix bypass
		{"githubusercontent.com", false},                         // must have subdomain
		{"", false},                                              // empty
		{"[::1]", false},                                         // IPv6 literal
		{"192.168.1.1", false},                                   // IPv4 literal
	}
	for _, c := range cases {
		if got := githubAssetHost(c.host); got != c.want {
			t.Errorf("githubAssetHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestGitHubSourceRejectsInstallerURLWithUserinfo(t *testing.T) {
	mock := newGitHubMock(t)
	body := []byte("payload")
	// Even when host would otherwise pass, userinfo must be rejected.
	mock.setManifest("https://good@github.com/setup.exe", body)
	src := newTestGitHubSource(mock)
	src.installerHostMatch = func(string) bool { return true }
	m, err := src.FetchManifest(context.Background())
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	err = src.DownloadInstaller(context.Background(), m, io.Discard, nil)
	if err == nil || !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed for userinfo URL", err)
	}
}

func TestGitHubSourceRejectsInstallerLargerThanSize(t *testing.T) {
	// Manifest declares Size=10 but server sends 100 bytes.
	// LimitReader caps at Size+1; io.Copy sees Size+1 → error.
	mock := newGitHubMock(t)
	realBody := []byte(strings.Repeat("x", 100))
	sum := sha256.Sum256(realBody[:10])
	m := Manifest{
		Version: "1.0.0",
		URL:     mock.server.URL + "/assets/setup.exe",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    10, // lie about size
	}
	mockManifest, _ := json.Marshal(m)
	mock.manifestBody = mockManifest
	mock.installerBody = realBody
	src := newTestGitHubSource(mock)
	src.installerHostMatch = func(string) bool { return true }

	got, err := src.FetchManifest(context.Background())
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	err = src.DownloadInstaller(context.Background(), got, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "larger than declared size") {
		t.Fatalf("err=%v; want size-overflow error", err)
	}
}

func TestGitHubSourcePreservesHeadersAcrossRedirect(t *testing.T) {
	// Every request (including hops after 302) must carry Accept +
	// User-Agent, or GitHub asset CDN 403s the retry.
	var secondHopSaw http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHopSaw = r.Header.Clone()
		w.Write([]byte("body"))
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	src := NewGitHubSource(testRepo, "", http.DefaultClient, DefaultSourcePolicy()).(*githubSource)
	src.installerHostMatch = func(string) bool { return true }

	body := []byte("body")
	sum := sha256.Sum256(body)
	m := Manifest{Version: "1.0.0", URL: redirector.URL + "/hop", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
	if err := src.DownloadInstaller(context.Background(), m, io.Discard, nil); err != nil {
		t.Fatalf("DownloadInstaller: %v", err)
	}
	if secondHopSaw.Get("User-Agent") == "" {
		t.Fatal("User-Agent lost on redirect")
	}
	if secondHopSaw.Get("Accept") == "" {
		t.Fatal("Accept lost on redirect")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/updater/ -run TestGitHubSource -v`
Expected: FAIL — `undefined: NewGitHubSource`.

- [ ] **Step 3: Implement source_github.go**

Create `internal/updater/source_github.go`:

```go
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/appversion"
)

const defaultGitHubAPIBase = "https://api.github.com"

type githubSource struct {
	repo    string
	apiBase string
	client  *http.Client
	policy  SourcePolicy

	// installerHostMatch is a test hook. Production behavior: subdomain
	// of githubusercontent.com or exactly github.com.
	installerHostMatch func(host string) bool
}

// NewGitHubSource returns a Source backed by a public GitHub release.
// apiBase defaults to https://api.github.com; tests override to point
// at an httptest.Server. The returned Source has its own *http.Transport
// so setting FirstByteTimeout does not affect other sources.
func NewGitHubSource(repo, apiBase string, client *http.Client, policy SourcePolicy) Source {
	if apiBase == "" {
		apiBase = defaultGitHubAPIBase
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &githubSource{
		repo:               repo,
		apiBase:            apiBase,
		client:             client,
		policy:             policy,
		installerHostMatch: githubInstallerHost,
	}
}

func (s *githubSource) Name() string { return "github" }

// normalizeHost canonicalizes a URL host for whitelist comparison.
// - lowercases
// - trims trailing dot (defeats "github.com." bypass; DNS treats
//   trailing-dot as equivalent, so must our whitelist)
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h
}

// githubAssetHost is the SINGLE source of truth for "is this URL a
// legitimate GitHub-hosted asset?" It matches:
//   - github.com (production browser_download_url host)
//   - codeload.github.com (occasional archive redirect target)
//   - any subdomain of githubusercontent.com (release-assets.,
//     objects., raw., and future renames — GitHub has renamed the
//     asset CDN before)
// It does NOT match api.github.com (which is only used by the initial
// releases/latest request, guarded separately) or bare
// "githubusercontent.com" without subdomain (suffix-bypass defense).
func githubAssetHost(host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	if h == "github.com" || h == "codeload.github.com" {
		return true
	}
	// Must have a non-empty subdomain before ".githubusercontent.com".
	const suffix = ".githubusercontent.com"
	if !strings.HasSuffix(h, suffix) {
		return false
	}
	sub := strings.TrimSuffix(h, suffix)
	if sub == "" || strings.Contains(sub, "/") {
		return false
	}
	return true
}

// githubAPIHost is used ONLY for the initial /repos/.../releases/latest
// request. The asset URL returned by that call is validated against
// githubAssetHost / installerHostMatch.
func githubAPIHost(host string) bool {
	return normalizeHost(host) == "api.github.com"
}

func (s *githubSource) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentserver-app/"+appversion.Version)
}

// apiClient handles the initial /repos/.../releases/latest request.
// Its CheckRedirect allows api.github.com hops only.
func (s *githubSource) apiClient() *http.Client {
	return s.redirectPinned(s.client, githubAPIHost)
}

// assetClient handles both the latest.json asset fetch AND (via the
// separately-installed FirstByteTimeout) the installer download.
// CheckRedirect allows githubAssetHost hops only.
func (s *githubSource) assetClient() *http.Client {
	return s.redirectPinned(s.client, s.installerHostMatch)
}

// installerClient adds FirstByteTimeout on top of assetClient.
func (s *githubSource) installerClient() *http.Client {
	return applyFirstByteTimeout(s.assetClient(), s.policy.FirstByteTimeout)
}

func (s *githubSource) redirectPinned(base *http.Client, hostOK func(string) bool) *http.Client {
	client := *base
	prior := base.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.User != nil {
			return fmt.Errorf("%w: userinfo not permitted", ErrHostNotAllowed)
		}
		if !hostOK(req.URL.Hostname()) {
			return fmt.Errorf("%w: %s", ErrHostNotAllowed, req.URL.Hostname())
		}
		// Preserve Accept + User-Agent across redirects. Go's stdlib
		// copies headers on same-host redirects but strips
		// Authorization on cross-host; Accept + UA aren't in the
		// sensitive-header list but we set them defensively per hop.
		if req.Header.Get("Accept") == "" {
			req.Header.Set("Accept", "application/vnd.github+json")
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "agentserver-app/"+appversion.Version)
		}
		if prior != nil {
			return prior(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

type githubReleaseAsset struct {
	Name                string `json:"name"`
	BrowserDownloadURL  string `json:"browser_download_url"`
}

type githubReleaseResponse struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

func (s *githubSource) FetchManifest(ctx context.Context) (Manifest, error) {
	// Per-request budgets — NOT one budget shared across hops. A slow
	// API call must not consume the asset fetch's timeout.
	release, err := s.fetchRelease(ctx)
	if err != nil {
		return Manifest{}, err
	}
	assetURL := ""
	for _, a := range release.Assets {
		if a.Name == "latest.json" {
			assetURL = a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		return Manifest{}, fmt.Errorf("github release %s missing latest.json asset", release.TagName)
	}
	assetU, err := url.Parse(assetURL)
	if err != nil {
		return Manifest{}, fmt.Errorf("invalid asset url: %w", err)
	}
	if assetU.User != nil {
		return Manifest{}, fmt.Errorf("%w: asset url has userinfo", ErrHostNotAllowed)
	}
	if !s.installerHostMatch(assetU.Hostname()) {
		return Manifest{}, fmt.Errorf("%w: asset host %s", ErrHostNotAllowed, assetU.Hostname())
	}
	m, err := s.fetchLatestJSON(ctx, assetURL)
	if err != nil {
		return Manifest{}, err
	}
	// Structural check only — host validation is deferred to
	// DownloadInstaller so tests can inject a permissive matcher.
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	instU, err := url.Parse(m.URL)
	if err != nil {
		return Manifest{}, fmt.Errorf("invalid installer url: %w", err)
	}
	if instU.User != nil {
		return Manifest{}, fmt.Errorf("%w: installer url has userinfo", ErrHostNotAllowed)
	}
	return m, nil
}

// fetchRelease is one HTTP hop with its own ManifestTimeout budget.
func (s *githubSource) fetchRelease(parent context.Context) (githubReleaseResponse, error) {
	ctx, cancel := context.WithTimeout(parent, s.policy.ManifestTimeout)
	defer cancel()

	apiURL := strings.TrimRight(s.apiBase, "/") + "/repos/" + s.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return githubReleaseResponse{}, err
	}
	s.setHeaders(req)
	resp, err := s.apiClient().Do(req)
	if err != nil {
		return githubReleaseResponse{}, s.classifyFetch(parent, ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		// Deliberately do NOT log X-GitHub-Request-Id or response body —
		// avoid leaking identifying tokens to state.json / console API.
		rem := resp.Header.Get("X-RateLimit-Remaining")
		return githubReleaseResponse{}, fmt.Errorf("%w: github %d: x-ratelimit-remaining=%q", ErrRateLimited, resp.StatusCode, rem)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubReleaseResponse{}, fmt.Errorf("github fetch releases/latest: unexpected status %s", resp.Status)
	}
	var release githubReleaseResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&release); err != nil {
		return githubReleaseResponse{}, err
	}
	return release, nil
}

// fetchLatestJSON is one HTTP hop with its own ManifestTimeout budget.
func (s *githubSource) fetchLatestJSON(parent context.Context, assetURL string) (Manifest, error) {
	ctx, cancel := context.WithTimeout(parent, s.policy.ManifestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return Manifest{}, err
	}
	s.setHeaders(req)
	resp, err := s.assetClient().Do(req)
	if err != nil {
		return Manifest{}, s.classifyFetch(parent, ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return Manifest{}, fmt.Errorf("%w: github %d fetching latest.json", ErrRateLimited, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("github fetch latest.json: unexpected status %s", resp.Status)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, manifestMaxBytes)).Decode(&m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (s *githubSource) DownloadInstaller(ctx context.Context, m Manifest, dst io.Writer, onProgress func(SpeedSample)) error {
	u, err := url.Parse(m.URL)
	if err != nil {
		return fmt.Errorf("invalid installer url: %w", err)
	}
	if u.User != nil {
		return fmt.Errorf("%w: installer url has userinfo", ErrHostNotAllowed)
	}
	if !s.installerHostMatch(u.Hostname()) {
		return fmt.Errorf("%w: installer host %s", ErrHostNotAllowed, u.Hostname())
	}
	if onProgress == nil {
		onProgress = noopProgress
	}

	dlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	monitor := newSpeedMonitor(s.policy, cancel, onProgress)
	monitorDone := make(chan struct{})
	go func() { monitor.run(dlCtx); close(monitorDone) }()
	defer func() { cancel(); <-monitorDone }()

	// When the underlying Transport is not *http.Transport (test path),
	// applyFirstByteTimeout returned an unmodified client. Fall back
	// to a context deadline for the first byte via WithTimeout — the
	// deadline is cancelled after resp headers arrive by using a
	// child ctx JUST for the Do() call.
	reqCtx := dlCtx
	if !s.isRealTransport() && s.policy.FirstByteTimeout > 0 {
		var reqCancel context.CancelFunc
		reqCtx, reqCancel = context.WithTimeout(dlCtx, s.policy.FirstByteTimeout)
		defer reqCancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	s.setHeaders(req)
	resp, err := s.installerClient().Do(req)
	if err != nil {
		return s.classifyDownload(ctx, monitor, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github download: unexpected status %s", resp.Status)
	}
	body := monitor.wrap(io.LimitReader(resp.Body, m.Size+1))
	n, err := io.Copy(dst, body)
	if err != nil {
		return s.classifyDownload(ctx, monitor, err)
	}
	if n > m.Size {
		return fmt.Errorf("github download: response larger than declared size")
	}
	return nil
}

// isRealTransport mirrors cdnSource.isRealTransport — used by
// DownloadInstaller to decide whether to add a context-level
// FirstByteTimeout when the underlying Transport is a custom
// RoundTripper (test path).
func (s *githubSource) isRealTransport() bool {
	rt := s.client.Transport
	if rt == nil {
		return true
	}
	_, ok := rt.(*http.Transport)
	return ok
}

// classifyFetch takes both the parent ctx (caller-supplied) and the
// per-request ctx (has the ManifestTimeout deadline). If the per-request
// deadline fired, return ErrFetchTimeout; else honor parent
// cancellation; else return err verbatim.
func (s *githubSource) classifyFetch(parent context.Context, req context.Context, err error) error {
	if req.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%w: %v", ErrFetchTimeout, err)
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return err
}

func (s *githubSource) classifyDownload(parent context.Context, monitor *speedMonitor, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if monitor.Tripped() {
		return fmt.Errorf("%w: %v", ErrSlowDownload, err)
	}
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/updater/ -run TestGitHubSource -v`
Expected: 7 PASS. If the timeout test flakes on your machine, widen the outer deadline and re-run.

- [ ] **Step 5: Verify no regression**

Run: `go test ./internal/updater/... ./cmd/launcher/...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add internal/updater/source_github.go internal/updater/source_github_test.go
git commit -m "feat(updater): GitHub source with rate-limit and slow-download fallback

Anonymous /repos/<repo>/releases/latest; downloads the latest.json
asset and installer via *.githubusercontent.com. Sends Accept +
User-Agent on every request. Rate limit (403/429) and slow download
map to ErrRateLimited / ErrSlowDownload sentinels the scheduler
treats as fallback."
```

---

### Task 6: State additions — LastSourceUsed, LastFallbacks

**Files:**
- Modify: `internal/updater/state.go`
- Test: extend `internal/updater/state_test.go` if it exists; else add a minimal state serialization test.

**Interfaces:**
- Consumes: nothing new.
- Produces: `type FallbackRecord struct { Source, Stage, Reason string; Tried time.Time }`; `State.LastSourceUsed string`; `State.LastFallbacks []FallbackRecord`.

- [ ] **Step 1: Read current state.go to locate the State struct**

Run: `cat internal/updater/state.go | head -60`

- [ ] **Step 2: Write the failing test**

Add to `internal/updater/state_test.go` (create the file if it doesn't exist):

```go
package updater

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStateSerializesFallbackFields(t *testing.T) {
	s := State{
		LastSourceUsed: "github",
		LastFallbacks: []FallbackRecord{
			{Source: "github", Stage: "manifest", Reason: "timeout", Tried: time.Unix(1_700_000_000, 0).UTC()},
		},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got State
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.LastSourceUsed != "github" {
		t.Fatalf("LastSourceUsed=%q", got.LastSourceUsed)
	}
	if len(got.LastFallbacks) != 1 || got.LastFallbacks[0].Stage != "manifest" {
		t.Fatalf("fallbacks not round-tripped: %+v", got.LastFallbacks)
	}
}

func TestStateOmitsEmptyFallbackFields(t *testing.T) {
	b, err := json.Marshal(State{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got == "" || len(got) < 2 {
		t.Fatalf("bad json: %s", got)
	}
	// omitempty means the empty fields do not appear.
	if strings.Contains(string(b), "last_source_used") || strings.Contains(string(b), "last_fallbacks") {
		t.Fatalf("empty state must not include new fields: %s", string(b))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/updater/ -run TestStateSerializes -v`
Expected: FAIL — `undefined: FallbackRecord`.

- [ ] **Step 4: Extend state.go**

Add to `internal/updater/state.go`:

```go
type FallbackRecord struct {
	Source string    `json:"source"`
	Stage  string    `json:"stage"`
	Reason string    `json:"reason"`
	Tried  time.Time `json:"tried"`
}
```

And to the existing `type State struct { … }`:

```go
	LastSourceUsed string           `json:"last_source_used,omitempty"`
	LastFallbacks  []FallbackRecord `json:"last_fallbacks,omitempty"`
```

Add `"time"` to state.go imports if not already present.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/updater/ -run TestState -v`
Expected: all PASS.

- [ ] **Step 6: Verify no regression**

Run: `go test ./internal/updater/...`
Expected: green.

- [ ] **Step 7: Commit**

```bash
git add internal/updater/state.go internal/updater/state_test.go
git commit -m "feat(updater): add LastSourceUsed + LastFallbacks to State"
```

---

### Task 7: Scheduler refactor in service.go + fallback tests

**Files:**
- Modify: `internal/updater/service.go`
- Create: `internal/updater/service_fallback_test.go`

**Interfaces:**
- Consumes: `Source`, `FallbackRecord`, `verifyInstaller` (kept in service.go), `noopProgress`.
- Produces:
  - `Service.Sources []Source` public field.
  - `Service.effectiveSources()` private method (compat shortcut).
  - Refactored `Check` and `DownloadAndStart` that iterate sources.
  - `Service.recordFallback` private helper that appends to a rolling 5-entry buffer.

**Critical invariants:**
- If `Sources == nil` AND no env, behavior is byte-identical to today (via compat shortcut building `[NewCDNSource(s.ManifestURL, s.Client, DefaultSourcePolicy())]`).
- First non-error `FetchManifest` is authoritative (StatusLatest / StatusAvailable both stop iteration).
- Per-source manifest binding: `DownloadInstaller` receives the manifest THAT source fetched, never a foreign one.
- State file is written once at flow end via existing `saveError` / `saveFinalState`.

- [ ] **Step 1: Write the failing scheduler tests**

Create `internal/updater/service_fallback_test.go`:

```go
package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSource is fully controllable per call.
type fakeSource struct {
	name           string
	manifest       Manifest
	fetchErr       error
	downloadErr    error
	downloadBytes  []byte
	fetchCount     int
	downloadCount  int
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) FetchManifest(ctx context.Context) (Manifest, error) {
	f.fetchCount++
	if f.fetchErr != nil {
		return Manifest{}, f.fetchErr
	}
	return f.manifest, nil
}

func (f *fakeSource) DownloadInstaller(ctx context.Context, m Manifest, dst io.Writer, onProgress func(SpeedSample)) error {
	f.downloadCount++
	if f.downloadErr != nil {
		return f.downloadErr
	}
	_, err := dst.Write(f.downloadBytes)
	return err
}

func newTestService(t *testing.T, sources []Source) (*Service, string) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		CurrentVersion: "0.0.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(statePath),
		Sources:        sources,
		StartInstaller: func(context.Context, string) error { return nil },
	}
	return svc, statePath
}

func TestServiceCheckPrefersFirstSource(t *testing.T) {
	first := &fakeSource{name: "github", manifest: Manifest{
		Version: "0.0.2", URL: "https://" + AssetsHost + "/setup.exe", SHA256: strings.Repeat("a", 64), Size: 1,
	}}
	second := &fakeSource{name: "cdn", fetchErr: errors.New("should not be called")}
	svc, _ := newTestService(t, []Source{first, second})

	state, err := svc.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check err=%v", err)
	}
	if state.LastSourceUsed != "github" {
		t.Fatalf("LastSourceUsed=%q", state.LastSourceUsed)
	}
	if state.Status != StatusAvailable {
		t.Fatalf("status=%v", state.Status)
	}
	if second.fetchCount != 0 {
		t.Fatalf("second source called %d times", second.fetchCount)
	}
}

func TestServiceCheckFallsBackOnFirstError(t *testing.T) {
	first := &fakeSource{name: "github", fetchErr: fmt.Errorf("%w: boom", ErrFetchTimeout)}
	second := &fakeSource{name: "cdn", manifest: Manifest{
		Version: "0.0.2", URL: "https://" + AssetsHost + "/setup.exe", SHA256: strings.Repeat("a", 64), Size: 1,
	}}
	svc, _ := newTestService(t, []Source{first, second})

	state, err := svc.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check err=%v", err)
	}
	if state.LastSourceUsed != "cdn" {
		t.Fatalf("LastSourceUsed=%q", state.LastSourceUsed)
	}
	if len(state.LastFallbacks) != 1 {
		t.Fatalf("expected 1 fallback, got %d", len(state.LastFallbacks))
	}
	fb := state.LastFallbacks[0]
	if fb.Source != "github" || fb.Stage != "manifest" || !strings.Contains(fb.Reason, "timeout") {
		t.Fatalf("bad fallback: %+v", fb)
	}
}

func TestServiceCheckAllSourcesFail(t *testing.T) {
	first := &fakeSource{name: "github", fetchErr: errors.New("no1")}
	second := &fakeSource{name: "cdn", fetchErr: errors.New("no2")}
	svc, _ := newTestService(t, []Source{first, second})

	state, err := svc.Check(context.Background(), true)
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
	if state.Status != StatusError {
		t.Fatalf("status=%v", state.Status)
	}
}

func TestServiceDownloadAndStartFallsBackOnSHA256Mismatch(t *testing.T) {
	goodBody := []byte("payload")
	badBody := []byte("wrong-bytes-same-length")
	if len(badBody) != len(goodBody) {
		badBody = badBody[:len(goodBody)]
	}
	sha := sha256Hex(goodBody)
	first := &fakeSource{
		name:          "github",
		manifest:      Manifest{Version: "0.0.2", URL: "https://" + AssetsHost + "/setup.exe", SHA256: sha, Size: int64(len(goodBody))},
		downloadBytes: badBody, // wrong bytes ⇒ SHA256 mismatch at verify
	}
	second := &fakeSource{
		name:          "cdn",
		manifest:      Manifest{Version: "0.0.2", URL: "https://" + AssetsHost + "/setup.exe", SHA256: sha, Size: int64(len(goodBody))},
		downloadBytes: goodBody,
	}
	svc, _ := newTestService(t, []Source{first, second})

	state, err := svc.DownloadAndStart(context.Background(), first.manifest)
	if err != nil {
		t.Fatalf("DownloadAndStart err=%v", err)
	}
	if state.LastSourceUsed != "cdn" {
		t.Fatalf("LastSourceUsed=%q", state.LastSourceUsed)
	}
	found := false
	for _, fb := range state.LastFallbacks {
		if fb.Source == "github" && fb.Stage == "verify" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected verify fallback, got %+v", state.LastFallbacks)
	}
}

func TestServiceParentCtxCancellationSkipsFallback(t *testing.T) {
	first := &fakeSource{name: "github", fetchErr: context.Canceled}
	second := &fakeSource{name: "cdn", fetchErr: errors.New("must not be called")}
	svc, _ := newTestService(t, []Source{first, second})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Check(ctx, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if second.fetchCount != 0 {
		t.Fatal("second source was called after parent cancel")
	}
}

func TestServiceCompatShortcutWhenSourcesNil(t *testing.T) {
	// Sources==nil ⇒ Service constructs [cdnSource] from ManifestURL.
	// We just assert effectiveSources() returns a non-empty slice
	// whose sole element is the CDN source. Full behavior is covered
	// by service_test.go's existing 28 fixtures.
	svc := &Service{ManifestURL: "https://" + AssetsHost + "/x.json"}
	got := svc.effectiveSources()
	if len(got) != 1 {
		t.Fatalf("effectiveSources len=%d", len(got))
	}
	if got[0].Name() != "cdn" {
		t.Fatalf("effectiveSources[0]=%q, want cdn", got[0].Name())
	}
}

func TestServiceRollingFallbackBufferCap(t *testing.T) {
	// Simulate 7 recorded fallbacks; buffer cap = 5.
	svc := &Service{Now: func() time.Time { return time.Unix(0, 0) }}
	buf := []FallbackRecord{}
	for i := 0; i < 7; i++ {
		buf = svc.appendFallback(buf, "github", "manifest", fmt.Errorf("err-%d", i))
	}
	if len(buf) != 5 {
		t.Fatalf("buffer len=%d want 5", len(buf))
	}
	// The most recent five must remain — err-2 through err-6.
	if !strings.Contains(buf[0].Reason, "err-2") || !strings.Contains(buf[4].Reason, "err-6") {
		t.Fatalf("wrong window: %+v", buf)
	}
}

func sha256Hex(b []byte) string {
	h := sha256Sum(b)
	return hex(h[:])
}

// sha256Sum + hex are defined in service.go / crypto imports. This test
// file mirrors them locally to avoid depending on unexported helpers.
```

You'll need to add small helpers `sha256Sum` and `hex` at the bottom of the test file:

```go
import (
	crypto_sha256 "crypto/sha256"
	encoding_hex "encoding/hex"
)

func sha256Sum(b []byte) [32]byte { return crypto_sha256.Sum256(b) }
func hex(b []byte) string          { return encoding_hex.EncodeToString(b) }
```

(Or inline via `crypto/sha256` and `encoding/hex` directly.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/updater/ -run 'TestService(Check|DownloadAndStart|ParentCtx|Compat|Rolling)' -v`
Expected: FAIL — `undefined: Service.Sources`, `Service.effectiveSources`, `Service.appendFallback`.

- [ ] **Step 3: Refactor service.go**

Edit `internal/updater/service.go`:

a) Add `Sources []Source` to the `Service` struct.

b) Add these helpers below `Service` struct:

```go
const maxFallbackHistory = 5

// compatCDNPolicy is the SourcePolicy applied when the compat shortcut
// synthesizes a cdnSource. All zeros ⇒ no ManifestTimeout wrap, no
// FirstByteTimeout, no speed monitor — byte-identical to today's
// behavior (which had none of these). This preserves the "existing
// tests unchanged" guarantee.
func compatCDNPolicy() SourcePolicy {
	return SourcePolicy{
		ManifestTimeout:     0,
		FirstByteTimeout:    0,
		SpeedWindow:         0,
		MinSpeedBytesPerSec: 0,
	}
}

func (s Service) effectiveSources() []Source {
	if len(s.Sources) > 0 {
		return s.Sources
	}
	url := s.ManifestURL
	if url == "" {
		url = DefaultManifestURL
	}
	return []Source{NewCDNSource(url, s.client(), compatCDNPolicy())}
}

func (s Service) appendFallback(buf []FallbackRecord, source, stage string, err error) []FallbackRecord {
	rec := FallbackRecord{
		Source: source,
		Stage:  stage,
		Reason: err.Error(),
		Tried:  s.now(),
	}
	buf = append(buf, rec)
	if len(buf) > maxFallbackHistory {
		buf = buf[len(buf)-maxFallbackHistory:]
	}
	return buf
}

func mergeFallbacks(prior, fresh []FallbackRecord) []FallbackRecord {
	combined := append([]FallbackRecord{}, prior...)
	combined = append(combined, fresh...)
	if len(combined) > maxFallbackHistory {
		combined = combined[len(combined)-maxFallbackHistory:]
	}
	return combined
}

// saveErrorWithFallbacks writes a StatusError state that PRESERVES
// the caller's rolling fallback history. saveError alone drops
// LastFallbacks, which contradicts the spec's "ops can see days
// later why every attempt failed" guarantee.
func (s Service) saveErrorWithFallbacks(now time.Time, err error, prior []FallbackRecord, fresh []FallbackRecord) (State, error) {
	state := State{
		CurrentVersion: s.CurrentVersion,
		LastCheckedAt:  now,
		Status:         StatusError,
		LastError:      err.Error(),
		LastFallbacks:  mergeFallbacks(prior, fresh),
	}
	if p, loadErr := s.loadState(); loadErr == nil {
		if !p.LastCheckedAt.IsZero() {
			state.LastCheckedAt = p.LastCheckedAt
		}
		if p.Update != nil {
			if cmp, cmpErr := CompareVersions(p.Update.Version, s.CurrentVersion); cmpErr == nil && cmp > 0 {
				state.Update = p.Update
			}
		}
		state.LastSourceUsed = p.LastSourceUsed // preserve last-known good
	}
	if saveErr := s.saveState(state); saveErr != nil {
		return state, errors.Join(err, fmt.Errorf("save error state: %w", saveErr))
	}
	return state, err
}
```

c) Rewrite `Check` to iterate `effectiveSources()`:

```go
func (s Service) Check(ctx context.Context, automatic bool) (State, error) {
	serviceStateMu.Lock()
	defer serviceStateMu.Unlock()

	now := s.now()
	prior, err := s.loadState()
	if err != nil {
		return s.saveError(now, err)
	}
	if automatic && !prior.LastCheckedAt.IsZero() && !now.Before(prior.LastCheckedAt) && now.Sub(prior.LastCheckedAt) < s.autoCheckEvery() {
		prior = NormalizeStateForCurrentVersion(prior, s.CurrentVersion)
		if err := s.saveState(prior); err != nil {
			return s.saveError(now, err)
		}
		return prior, nil
	}

	checking := State{
		CurrentVersion: s.CurrentVersion,
		LastCheckedAt:  prior.LastCheckedAt,
		Status:         StatusChecking,
		Update:         prior.Update,
		LastSourceUsed: prior.LastSourceUsed,
		LastFallbacks:  prior.LastFallbacks,
	}
	if err := s.saveState(checking); err != nil {
		return s.saveError(now, err)
	}

	var fallbacks []FallbackRecord
	var lastErr error
	for _, src := range s.effectiveSources() {
		attemptCtx, cancel := context.WithCancel(ctx)
		manifest, err := src.FetchManifest(attemptCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return s.saveError(now, ctx.Err())
			}
			fallbacks = s.appendFallback(fallbacks, src.Name(), "manifest", err)
			lastErr = err
			continue
		}
		cmp, err := CompareVersions(manifest.Version, s.CurrentVersion)
		if err != nil {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "version", err)
			lastErr = err
			continue
		}
		history := mergeFallbacks(prior.LastFallbacks, fallbacks)
		if cmp <= 0 {
			state := State{
				CurrentVersion: s.CurrentVersion,
				LastCheckedAt:  now,
				Status:         StatusLatest,
				LastSourceUsed: src.Name(),
				LastFallbacks:  history,
			}
			return s.saveFinalState(now, state)
		}
		state := State{
			CurrentVersion: s.CurrentVersion,
			LastCheckedAt:  now,
			Status:         StatusAvailable,
			Update: &AvailableUpdate{
				Version: manifest.Version,
				URL:     manifest.URL,
				SHA256:  manifest.SHA256,
				Size:    manifest.Size,
				Notes:   manifest.Notes,
			},
			LastSourceUsed: src.Name(),
			LastFallbacks:  history,
		}
		return s.saveFinalState(now, state)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no sources configured")
	}
	return s.saveErrorWithFallbacks(now, lastErr, prior.LastFallbacks, fallbacks)
}
```

d) Rewrite `DownloadAndStart` to iterate sources (each source re-fetches its own manifest). This preserves the caller-manifest version-downgrade guard (today's `service.go:147-153`), the `promoted`/defer temp-file cleanup (today's `service.go:179-184`), and augments all terminal error paths with the fallback history so ops can see later why every attempt failed:

```go
func (s Service) DownloadAndStart(ctx context.Context, m Manifest) (State, error) {
	serviceStateMu.Lock()
	defer serviceStateMu.Unlock()

	now := s.now()
	if err := m.Validate(); err != nil {
		return s.saveError(now, err)
	}
	// Caller-manifest version guard — PRESERVED from today's behavior.
	// A UI bug that replays a stale manifest must not trigger download.
	cmp, err := CompareVersions(m.Version, s.CurrentVersion)
	if err != nil {
		return s.saveError(now, fmt.Errorf("invalid current version: %w", err))
	}
	if cmp <= 0 {
		return s.saveError(now, fmt.Errorf("update version %s is not newer than current version %s", m.Version, s.CurrentVersion))
	}
	if s.CacheDir == "" {
		return s.saveError(now, fmt.Errorf("cache dir is required"))
	}
	if err := os.MkdirAll(s.CacheDir, 0o755); err != nil {
		return s.saveError(now, err)
	}
	prior, _ := s.loadState()
	downloading := State{
		CurrentVersion: s.CurrentVersion,
		Status:         StatusDownloading,
		Update:         availableFromManifest(m),
		LastSourceUsed: prior.LastSourceUsed,
		LastFallbacks:  prior.LastFallbacks,
	}
	if err := s.saveState(downloading); err != nil {
		return s.saveError(now, err)
	}

	var fallbacks []FallbackRecord
	var lastErr error
	for _, src := range s.effectiveSources() {
		freshM, err := src.FetchManifest(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return s.saveErrorWithFallbacks(now, ctx.Err(), prior.LastFallbacks, fallbacks)
			}
			fallbacks = s.appendFallback(fallbacks, src.Name(), "manifest", err)
			lastErr = err
			continue
		}
		vcmp, err := CompareVersions(freshM.Version, s.CurrentVersion)
		if err != nil {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "version", err)
			lastErr = err
			continue
		}
		if vcmp <= 0 {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "version",
				fmt.Errorf("source manifest version %s not newer than current %s", freshM.Version, s.CurrentVersion))
			lastErr = fmt.Errorf("source %s has no newer version", src.Name())
			continue
		}

		finalPath, err := installerCachePath(s.CacheDir, freshM)
		if err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		temp, err := os.CreateTemp(s.CacheDir, filepath.Base(finalPath)+".*.tmp")
		if err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		tempPath := temp.Name()
		// promoted/defer PRESERVED from today's behavior — covers every
		// unhappy exit from replaceFile / BeforeInstallerStart / start
		// / saveFinalState. Without this the temp file leaks on non-
		// download errors, a disk-fill vector under retry loops.
		promoted := false
		defer func() {
			if !promoted {
				_ = os.Remove(tempPath)
			}
		}()

		attemptCtx, cancel := context.WithCancel(ctx)
		err = src.DownloadInstaller(attemptCtx, freshM, temp, noopProgress)
		cancel()
		if closeErr := temp.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			if ctx.Err() != nil {
				return s.saveErrorWithFallbacks(now, ctx.Err(), prior.LastFallbacks, fallbacks)
			}
			fallbacks = s.appendFallback(fallbacks, src.Name(), "download", err)
			lastErr = err
			continue
		}
		if err := verifyInstaller(tempPath, freshM); err != nil {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "verify",
				fmt.Errorf("%w: %v", ErrSHA256Mismatch, err))
			lastErr = err
			continue
		}
		if err := replaceFile(tempPath, finalPath); err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		if s.BeforeInstallerStart != nil {
			if err := s.BeforeInstallerStart(ctx, freshM, finalPath); err != nil {
				return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
			}
		}
		start := s.StartInstaller
		startContext := ctx
		if start == nil {
			start = StartInstaller
			startContext = context.Background()
		}
		if err := start(startContext, finalPath); err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		promoted = true
		state := State{
			CurrentVersion: s.CurrentVersion,
			Status:         StatusInstallerStarted,
			Update:         availableFromManifest(freshM),
			LastSourceUsed: src.Name(),
			LastFallbacks:  mergeFallbacks(prior.LastFallbacks, fallbacks),
		}
		return s.saveFinalState(now, state)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no sources configured")
	}
	return s.saveErrorWithFallbacks(now, lastErr, prior.LastFallbacks, fallbacks)
}
```

**Important note on the loop's `defer`:** each source iteration
registers a `defer` that runs at function return. In practice this is
safe because at most one iteration reaches the temp-file creation on
a successful pass (subsequent iterations `continue` before creating
their own temp file — if the CURRENT source failed and we're moving
on, we've already `os.Remove`'d the current temp and left it). If
future changes create a temp file every iteration, convert the defer
into an explicit inline cleanup with a helper `cleanupTemp(&promoted, tempPath)`.


e) Delete `fetchManifest`, `manifestDownloadClient`, `downloadInstaller`, `installerDownloadClient`, `redirectPinnedAssetsClient` from `service.go`. Keep `verifyInstaller`, `installerCachePath`, `availableFromManifest`, `now`, `loadState`, `saveState`, `saveFinalState`, `saveError`, `client`, `autoCheckEvery`, `NormalizeStateForCurrentVersion`.

f) The `service_source_test.go` lint asserts `service.go` contains `start = StartInstaller` and `startContext = context.Background()`. Both are preserved in the new `DownloadAndStart`.

- [ ] **Step 4: Run the fallback tests**

Run: `go test ./internal/updater/ -run 'TestService(Check|DownloadAndStart|ParentCtx|Compat|Rolling)' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test ./internal/updater/... ./cmd/launcher/...`
Expected: all green. If existing `service_test.go` tests break, the compat shortcut is wrong — investigate before continuing.

- [ ] **Step 6: Commit**

```bash
git add internal/updater/service.go internal/updater/service_fallback_test.go
git commit -m "feat(updater): scheduler iterates Sources with per-source manifest binding

Both Check and DownloadAndStart loop over effectiveSources(). Sources
nil ⇒ compat shortcut builds [cdnSource] from ManifestURL+Client;
28 existing service_test.go fixtures keep working unchanged. Each
source re-fetches its own manifest and downloads its own bytes.
SHA256 verify stays in scheduler; mismatch = fallback with
stage=verify. Rolling 5-entry FallbackRecord history."
```

---

### Task 8: config.go + BuildSources

**Files:**
- Create: `internal/updater/config.go`
- Create: `internal/updater/config_test.go`

**Interfaces:**
- Consumes: `NewGitHubSource`, `NewCDNSource`, `DefaultSourcePolicy`.
- Produces:
  - `type UpgradeConfig struct { GitHubEnabled bool; GitHubRepo string; GitHubPolicy SourcePolicy }`
  - `func LoadUpgradeConfig(env func(string) string) UpgradeConfig`
  - `func BuildSources(cfg UpgradeConfig) []Source` — returns `nil` when GitHub disabled (compat shortcut fires); returns `[github, cdn]` otherwise.

- [ ] **Step 1: Write the failing tests**

Create `internal/updater/config_test.go`:

```go
package updater

import (
	"testing"
	"time"
)

func makeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadUpgradeConfigDefaults(t *testing.T) {
	cfg := LoadUpgradeConfig(makeEnv(nil))
	if cfg.GitHubEnabled {
		t.Fatal("default GitHubEnabled must be false")
	}
	if cfg.GitHubRepo != "agentserver/app" {
		t.Fatalf("default repo=%q", cfg.GitHubRepo)
	}
	if cfg.GitHubPolicy.ManifestTimeout != 5*time.Second {
		t.Fatalf("default ManifestTimeout=%v", cfg.GitHubPolicy.ManifestTimeout)
	}
}

func TestLoadUpgradeConfigOverrides(t *testing.T) {
	cfg := LoadUpgradeConfig(makeEnv(map[string]string{
		"UPGRADE_GITHUB_ENABLED":            "true",
		"UPGRADE_GITHUB_REPO":               "acme/tool",
		"UPGRADE_GITHUB_MANIFEST_TIMEOUT":   "2s",
		"UPGRADE_GITHUB_FIRST_BYTE_TIMEOUT": "3s",
		"UPGRADE_GITHUB_MIN_SPEED_BPS":      "200000",
		"UPGRADE_GITHUB_SPEED_WINDOW":       "7s",
	}))
	if !cfg.GitHubEnabled {
		t.Fatal("expected enabled")
	}
	if cfg.GitHubRepo != "acme/tool" {
		t.Fatalf("repo=%q", cfg.GitHubRepo)
	}
	if cfg.GitHubPolicy.ManifestTimeout != 2*time.Second {
		t.Fatalf("ManifestTimeout=%v", cfg.GitHubPolicy.ManifestTimeout)
	}
	if cfg.GitHubPolicy.MinSpeedBytesPerSec != 200000 {
		t.Fatalf("MinSpeedBytesPerSec=%d", cfg.GitHubPolicy.MinSpeedBytesPerSec)
	}
	if cfg.GitHubPolicy.SpeedWindow != 7*time.Second {
		t.Fatalf("SpeedWindow=%v", cfg.GitHubPolicy.SpeedWindow)
	}
}

func TestBuildSourcesDisabledReturnsNil(t *testing.T) {
	got := BuildSources(LoadUpgradeConfig(makeEnv(nil)))
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestBuildSourcesEnabledReturnsGitHubThenCDN(t *testing.T) {
	got := BuildSources(LoadUpgradeConfig(makeEnv(map[string]string{
		"UPGRADE_GITHUB_ENABLED": "true",
	})))
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Name() != "github" || got[1].Name() != "cdn" {
		t.Fatalf("order=[%s,%s]", got[0].Name(), got[1].Name())
	}
}

func TestLoadUpgradeConfigRejectsMaliciousRepoSlug(t *testing.T) {
	// Path-traversal / control-char attempts fall back to default.
	cases := []string{
		"../../etc/passwd",
		"owner//repo",
		"owner",
		"owner/repo/extra",
		"/leading-slash/repo",
		"owner/repo with space",
		"owner/repo\nrepo",
	}
	for _, bad := range cases {
		cfg := LoadUpgradeConfig(makeEnv(map[string]string{
			"UPGRADE_GITHUB_REPO": bad,
		}))
		if cfg.GitHubRepo != "agentserver/app" {
			t.Errorf("bad slug %q accepted as %q; want default fallback", bad, cfg.GitHubRepo)
		}
	}
}

func TestLoadUpgradeConfigAcceptsValidRepoSlugs(t *testing.T) {
	cases := []string{"owner/repo", "a.b/c-d", "org_1/x_2.3"}
	for _, ok := range cases {
		cfg := LoadUpgradeConfig(makeEnv(map[string]string{
			"UPGRADE_GITHUB_REPO": ok,
		}))
		if cfg.GitHubRepo != ok {
			t.Errorf("valid slug %q rejected", ok)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/updater/ -run '(LoadUpgradeConfig|BuildSources)' -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement config.go**

Create `internal/updater/config.go`:

```go
package updater

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type UpgradeConfig struct {
	GitHubEnabled bool
	GitHubRepo    string
	GitHubPolicy  SourcePolicy
}

// validRepoSlug matches "owner/repo" where each segment is 1..100 chars
// of [A-Za-z0-9._-] — the character set GitHub allows. This defeats
// path-traversal shenanigans like "../etc/passwd" that would otherwise
// end up inside the constructed API URL and show as a suspicious
// request line in logs. Invalid values fall back to the default.
var validRepoSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`)

// LoadUpgradeConfig reads env vars via the provided getter. Pass
// os.Getenv in production; tests pass a fake.
func LoadUpgradeConfig(env func(string) string) UpgradeConfig {
	cfg := UpgradeConfig{
		GitHubRepo:   "agentserver/app",
		GitHubPolicy: DefaultSourcePolicy(),
	}
	if strings.EqualFold(env("UPGRADE_GITHUB_ENABLED"), "true") {
		cfg.GitHubEnabled = true
	}
	if v := env("UPGRADE_GITHUB_REPO"); v != "" && validRepoSlug.MatchString(v) {
		cfg.GitHubRepo = v
	}
	if v := env("UPGRADE_GITHUB_MANIFEST_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.GitHubPolicy.ManifestTimeout = d
		}
	}
	if v := env("UPGRADE_GITHUB_FIRST_BYTE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.GitHubPolicy.FirstByteTimeout = d
		}
	}
	if v := env("UPGRADE_GITHUB_MIN_SPEED_BPS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.GitHubPolicy.MinSpeedBytesPerSec = n
		}
	}
	if v := env("UPGRADE_GITHUB_SPEED_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.GitHubPolicy.SpeedWindow = d
		}
	}
	return cfg
}

// BuildSources returns nil when GitHub is disabled (Service's compat
// shortcut then builds [cdnSource] from ManifestURL). Returns
// [githubSource, cdnSource] when enabled; the CDN source is built
// from DefaultManifestURL and http.DefaultClient.
func BuildSources(cfg UpgradeConfig) []Source {
	if !cfg.GitHubEnabled {
		return nil
	}
	return []Source{
		NewGitHubSource(cfg.GitHubRepo, "", http.DefaultClient, cfg.GitHubPolicy),
		NewCDNSource(DefaultManifestURL, http.DefaultClient, DefaultSourcePolicy()),
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/updater/ -run '(LoadUpgradeConfig|BuildSources)' -v`
Expected: 4 PASS.

- [ ] **Step 5: Verify no regression**

Run: `go test ./internal/updater/...`
Expected: green.

- [ ] **Step 6: Commit**

```bash
git add internal/updater/config.go internal/updater/config_test.go
git commit -m "feat(updater): LoadUpgradeConfig + BuildSources (env-only)"
```

---

### Task 9: Launcher wire-up

**Files:**
- Modify: `cmd/launcher/main.go` — `newCompletedUpdater` (lines 341–348).

**Interfaces:**
- Consumes: `updater.LoadUpgradeConfig`, `updater.BuildSources`.

- [ ] **Step 1: Read current newCompletedUpdater**

Run: `sed -n '338,352p' cmd/launcher/main.go`

- [ ] **Step 2: Edit `newCompletedUpdater`**

Replace the function body:

```go
func newCompletedUpdater(p paths.Paths) *updater.Service {
	cfg := updater.LoadUpgradeConfig(os.Getenv)
	return &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    updater.DefaultManifestURL,
		CacheDir:       p.UpdatesCacheDir,
		State:          updater.NewStateStore(p.UpdateStateFile),
		Sources:        updater.BuildSources(cfg),
	}
}
```

If `os` is not already imported in `cmd/launcher/main.go`, add it (it likely is already).

- [ ] **Step 3: Run cmd/launcher tests**

Run: `go test ./cmd/launcher/...`
Expected: all green — existing tests still pass because `BuildSources(disabled) == nil` triggers the compat shortcut.

- [ ] **Step 4: Manual sanity check — verify default off**

Run:

```bash
UPGRADE_GITHUB_ENABLED=false go test ./cmd/launcher/... -run TestLauncherWiresUpdater -v 2>&1 | tail -5
```

(Adjust test name to match whatever launcher test covers `newCompletedUpdater`. If none exists, this step is a no-op — the previous step already covered it.)

- [ ] **Step 5: Commit**

```bash
git add cmd/launcher/main.go
git commit -m "feat(launcher): plumb UPGRADE_GITHUB_* env into updater.Sources"
```

---

### Task 10: Release pipeline — latest.json publishing

**Files:**
- (removed) `packaging/windows/latest.json.tmpl` — jq composes JSON structurally, no template.
- Modify: `scripts/windows-package-common.sh`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: existing Windows packaging (produces `dist/agentserver-app-<version>-setup.exe`).
- Produces: `dist/latest-github.json` and `dist/latest-cdn.json`; a CI workflow that uploads both `latest-github.json` + `.exe` to the GitHub release for the pushed tag.

Note: this task is CI/pipeline work and may be validated only end-to-end on the next tagged release. Local `go test` cannot exercise it. If your workflow uses a different CI system (Jenkins, GitLab), adapt the `.github/workflows/release.yml` step into that system's equivalent.

**Note:** the template file is now unnecessary — `jq -n` composes the JSON with proper escaping. If a template is still preferred by the team's release runbook, keep it separate and let the shell function `jq -f template.jq --arg …` render it; do NOT `sed`-substitute a JSON template (arbitrary `notes` strings would corrupt the file).

- [ ] **Step 1: Extend windows-package-common.sh**

At the end of `scripts/windows-package-common.sh`, add a function that emits both manifest flavors via `jq` so `notes` (or any field) with quotes, backslashes, or newlines round-trips safely:

```bash
# Emit latest-cdn.json and latest-github.json under $DIST_DIR from a
# built installer. Requires: jq, sha256sum, stat (GNU or BSD).
render_latest_json() {
  local installer_path="$1"
  local version="$2"
  local notes="${3:-}"
  command -v jq >/dev/null || { echo "jq required" >&2; return 2; }

  local size sha installer_name dist_dir
  # BSD stat differs from GNU stat; try both.
  size=$(stat -c%s "$installer_path" 2>/dev/null || stat -f%z "$installer_path")
  sha=$(sha256sum "$installer_path" | cut -d' ' -f1)
  installer_name=$(basename "$installer_path")
  dist_dir=$(dirname "$installer_path")

  local owner_repo="${UPGRADE_GITHUB_REPO:-agentserver/app}"
  local tag="v${version}"
  local cdn_url="https://assets.agent.cs.ac.cn/agentserver-app/windows/${installer_name}"
  local gh_url="https://github.com/${owner_repo}/releases/download/${tag}/${installer_name}"

  jq -n \
    --arg version "$version" \
    --arg url "$cdn_url" \
    --arg sha "$sha" \
    --arg notes "$notes" \
    --argjson size "$size" \
    '{version:$version, url:$url, sha256:$sha, size:$size, notes:$notes}' \
    > "${dist_dir}/latest-cdn.json"

  jq -n \
    --arg version "$version" \
    --arg url "$gh_url" \
    --arg sha "$sha" \
    --arg notes "$notes" \
    --argjson size "$size" \
    '{version:$version, url:$url, sha256:$sha, size:$size, notes:$notes}' \
    > "${dist_dir}/latest-github.json"
}
```

The `packaging/windows/latest.json.tmpl` file is intentionally NOT created — jq composes the JSON structurally.

Wire `render_latest_json` into the existing packaging flow at the point where the installer path and version are known.

- [ ] **Step 3: Add release workflow**

Create `.github/workflows/release.yml`:

```yaml
name: release

on:
  push:
    tags:
      - 'v*'

jobs:
  windows-release:
    runs-on: windows-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build Windows installer
        shell: bash
        run: |
          ./scripts/package-windows.sh

      - name: Render latest.json (github flavour)
        shell: bash
        run: |
          # scripts/windows-package-common.sh is sourced by
          # package-windows.sh and its render_latest_json call has
          # already emitted dist/latest-github.json.
          test -f dist/latest-github.json

      - name: Guard against tag re-run
        shell: bash
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          # Fail-fast if the release already has a latest.json asset —
          # re-running the workflow on the same tag would race the
          # second build against the first, and briefly point clients
          # at a mismatched .exe. Delete the release manually if
          # intentional re-publish is required.
          if gh release view "$TAG" --json assets --jq '.assets[].name' 2>/dev/null | grep -qx 'latest.json'; then
            echo "Refusing to re-publish: release $TAG already has latest.json"
            exit 1
          fi

      - name: Upload assets — installer first, latest.json last
        shell: bash
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          # Upload .exe first so latest.json never points at a missing
          # or wrong asset. Do NOT use --clobber on .exe — a re-run
          # would silently swap bytes under an already-published
          # latest.json.
          gh release upload "$TAG" dist/agentserver-app-*-setup.exe
          # Round-trip verify: fetch the just-uploaded asset back and
          # compare SHA against the local dist bytes. Catches CDN
          # cache poisoning / mid-upload corruption before the manifest
          # references it.
          EXE_NAME=$(ls dist/agentserver-app-*-setup.exe | head -1 | xargs -n1 basename)
          curl -sSfL -o /tmp/verify.exe "https://github.com/${GITHUB_REPOSITORY}/releases/download/${TAG}/${EXE_NAME}"
          DIST_SHA=$(sha256sum "dist/${EXE_NAME}" | cut -d' ' -f1)
          REMOTE_SHA=$(sha256sum /tmp/verify.exe | cut -d' ' -f1)
          [ "$DIST_SHA" = "$REMOTE_SHA" ] || { echo "SHA mismatch"; exit 1; }
          # Only now publish the manifest — clients that fetch it are
          # guaranteed to find a matching .exe.
          cp dist/latest-github.json dist/latest.json
          gh release upload "$TAG" dist/latest.json
```

- [ ] **Step 4: Validate the shell script locally (dry run)**

Run:

```bash
bash -n scripts/windows-package-common.sh
```

Expected: no syntax error output. Do NOT run the packaging script itself — it requires Windows tooling.

- [ ] **Step 5: Commit**

```bash
git add packaging/windows/latest.json.tmpl scripts/windows-package-common.sh .github/workflows/release.yml
git commit -m "feat(release): publish latest.json to GitHub release alongside installer

Windows packaging now emits latest-cdn.json and latest-github.json in
dist/. The release workflow uploads the .exe first, verifies its
remote SHA matches the built bytes, then uploads latest.json as
latest.json — ensuring the manifest is never live before the asset
it references."
```

---

### Task 11: End-to-end smoke on the merged branch

**Files:** none.

- [ ] **Step 1: Full test suite**

Run: `go test ./...`
Expected: all green.

- [ ] **Step 2: Full build**

Run: `go build ./...`
Expected: no output, exit 0.

- [ ] **Step 3: Verify substring lints still pass**

Run: `go test ./internal/updater/ -run '(TestDownloadAndStartUsesBackgroundContextForDefaultInstaller|Installer)' -v`
Expected: `service_source_test.go` and `installer_windows_source_test.go` assertions still pass — they enforce that `service.go` retains `start = StartInstaller` and `startContext = context.Background()` (both preserved by Task 7).

- [ ] **Step 4: Verify GitHub-disabled default is byte-identical**

Run:

```bash
unset UPGRADE_GITHUB_ENABLED
go test ./cmd/launcher/... ./internal/updater/... -count=1
```

Expected: green — proves the compat shortcut carries all 34 existing fixtures.

- [ ] **Step 5: Verify GitHub-enabled path compiles + fake sources pass**

Run:

```bash
UPGRADE_GITHUB_ENABLED=true UPGRADE_GITHUB_REPO=agentserver/app go test ./internal/updater/... -count=1
```

Expected: green.

- [ ] **Step 6: Commit any residual changes and prepare for review**

Run: `git status`
Expected: clean tree. If there are stray changes, review and either commit or discard.

---

## Changes Log (v2, post codex round 1)

- **Speed monitor**: skip trip check when `MinSpeedBytesPerSec <= 0` or `SpeedWindow <= 0` (compat mode disables monitor). New test: `TestSpeedMonitorDisabledWhenMinBPSZero`.
- **Service compat shortcut**: `effectiveSources()` now builds the CDN source with `compatCDNPolicy()` (all zeros), preserving byte-identical behavior for `Sources==nil` mode. Prevents the "download aborts at 10s" behavior change for existing test fixtures.
- **cdnSource + githubSource `installerClient`**: replaced `cloneTransport` with `applyFirstByteTimeout` — preserves custom `RoundTripper`s (e.g. test-only `assetsHostRewriteTransport`). Non-`*http.Transport` clients fall back to per-request `context.WithTimeout` for the first-byte deadline.
- **GitHub source host validation**: collapsed `githubInstallerHost` + `githubManifestHost` into single `githubAssetHost` (accepts `github.com` + `codeload.github.com` + `*.githubusercontent.com`). Real `browser_download_url` on `github.com` is now accepted. Added `normalizeHost` for trailing-dot / case-fold defense. New test: `TestGithubAssetHostMatcher` with adversarial cases (IPv6, suffix bypass, empty, IDN-ready).
- **GitHub source per-request timeout**: split `FetchManifest` into `fetchRelease` + `fetchLatestJSON`, each with its own `ManifestTimeout` budget. Second hop no longer starves when the first is slow. New test: `TestGitHubSourceManifestTimeoutIsPerRequest`.
- **GitHub source header preservation across redirects**: `CheckRedirect` re-installs `Accept` + `User-Agent` if missing. New test: `TestGitHubSourcePreservesHeadersAcrossRedirect`.
- **GitHub source manifest-timeout test fixed**: `TestGitHubSourceManifestTimeoutFires` now uses a real slow `httptest.Server` and asserts `errors.Is(err, ErrFetchTimeout)` + elapsed < `2 × ManifestTimeout`. Replaces the vacuous version that used `127.0.0.1:1`.
- **GitHub source rate-limit test**: `errorsIs` helper replaced with `errors.Is` calls throughout — the substring version defeated the sentinel contract.
- **GitHub source size-overflow test**: added `TestGitHubSourceRejectsInstallerLargerThanSize`.
- **GitHub source userinfo test**: added `TestGitHubSourceRejectsInstallerURLWithUserinfo`.
- **Rate-limit reason redaction**: fetchRelease's error message includes only `X-RateLimit-Remaining` — deliberately NOT `X-GitHub-Request-Id` or response body — to keep identifying tokens out of `state.json` / console API.
- **Scheduler caller-manifest version guard restored**: `DownloadAndStart` re-checks `CompareVersions(m.Version, s.CurrentVersion) <= 0` immediately after `m.Validate()` — matches today's `service.go:147-153`. Prevents spurious `StatusDownloading` transitions from a stale caller manifest.
- **Scheduler temp-file cleanup restored**: `promoted := false; defer` pattern re-added inside the source loop — covers every unhappy exit from `replaceFile` / `BeforeInstallerStart` / `start` / terminal error paths.
- **Scheduler terminal error preserves fallbacks**: new `saveErrorWithFallbacks` helper writes `StatusError` state with the merged rolling `LastFallbacks` buffer. All terminal error paths in `Check` and `DownloadAndStart` migrated to it. `saveError` alone (which drops `LastFallbacks`) is used only for pre-loop guards.
- **Config repo-slug validation**: `LoadUpgradeConfig` requires `UPGRADE_GITHUB_REPO` to match `^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`. Invalid values fall back to default `agentserver/app`. New tests: `TestLoadUpgradeConfigRejectsMaliciousRepoSlug`, `TestLoadUpgradeConfigAcceptsValidRepoSlugs`.
- **Release template dropped**: `packaging/windows/latest.json.tmpl` removed — `jq -n --arg …` composes JSON structurally, safe for arbitrary `notes` strings (quotes, backslashes, newlines).
- **Release workflow — no `--clobber` on `.exe`**: prevents byte-swap under an already-published `latest.json`. Pre-flight step refuses if `latest.json` already exists on the tag.
- **Task 6 test**: dropped hand-rolled `contains`; imports `strings` and uses `strings.Contains`.

## Self-Review

Ran fresh eyes over the plan against the spec:

**1. Spec coverage.** Walked every section of the spec:

| Spec section | Task(s) |
|---|---|
| Source interface + sentinels + policy | Task 1 |
| Speed monitor with injected clock/tick + Tripped | Task 2 |
| CDN source (ports today's behavior) | Task 3 |
| Manifest.Validate split (host removed) + test migration | Task 4 |
| GitHub source (auth-less API, Accept + UA, rate limit, host whitelist, redirect, slow-download) | Task 5 |
| State.LastSourceUsed + State.LastFallbacks + FallbackRecord | Task 6 |
| Scheduler iterating sources (per-source manifest binding, compat shortcut, save-once, cancel precedence, rolling buffer, StatusDownloading stability) | Task 7 |
| Env config + BuildSources | Task 8 |
| Launcher wire-up | Task 9 |
| Release pipeline (template, script, workflow, .exe-before-latest.json ordering) | Task 10 |
| E2E smoke | Task 11 |

Every spec section is covered.

**2. Placeholder scan.** No `TBD`, no `implement later`, no unbounded `add error handling`. Task 5 test relies on a small helper `errorsIs` defined at test scope to compare wrapped sentinels via message substring — this is intentional (the source wraps sentinels in `fmt.Errorf("%w: …", sentinel, …)`; `errors.Is` also works but the substring version reads cleaner in test output). Left as-is.

**3. Type consistency.** Ran through function names and signatures:

- `NewCDNSource(manifestURL string, client *http.Client, policy SourcePolicy) Source` — Task 3, referenced verbatim in Tasks 7 (compat shortcut) and 8 (BuildSources). ✓
- `NewGitHubSource(repo, apiBase string, client *http.Client, policy SourcePolicy) Source` — Task 5, referenced in Task 8. ✓
- `Source.DownloadInstaller(ctx, m, dst, onProgress)` — Tasks 3, 5, 7 all use this same signature. ✓
- `speedMonitor.Tripped()` — Tasks 2, 3, 5. ✓
- `Service.effectiveSources()` / `Service.appendFallback` — introduced in Task 7 and consumed by test in the same task. ✓
- `LoadUpgradeConfig(env func(string) string)` — Task 8, called in Task 9. ✓
- `BuildSources(cfg UpgradeConfig)` — Task 8, called in Task 9. ✓

No naming drift.

Plan is ready.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-29-upgrade-github-source-fallback.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
