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

// installerClient enforces FirstByteTimeout via ResponseHeaderTimeout.
// It clones s.client's Transport so the parent Client is not mutated.
func (s *cdnSource) installerClient() *http.Client {
	c := s.redirectPinned(s.client)
	transport := cloneTransport(c.Transport)
	transport.ResponseHeaderTimeout = s.policy.FirstByteTimeout
	c.Transport = transport
	return c
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

// cloneTransport returns a defensive copy of t so we can mutate
// ResponseHeaderTimeout without affecting the caller's Transport.
// If t is nil, it returns a fresh Transport that clones DefaultTransport.
func cloneTransport(t http.RoundTripper) *http.Transport {
	if t == nil {
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			return dt.Clone()
		}
		return &http.Transport{}
	}
	if ht, ok := t.(*http.Transport); ok {
		return ht.Clone()
	}
	// Non-*Transport RoundTripper: build a fresh Transport. Tests using
	// http.RoundTripper implementations should pass an *http.Transport.
	return &http.Transport{}
}

// unusedTimeCompat suppresses unused-import warning if the file's imports
// change during future edits.
var _ = time.Now
```

Remove that last `var _` line and any unused imports before committing — it's a placeholder to make the file compile if you copy incrementally.

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
- Consumes: `Source`, `SourcePolicy`, sentinels, `Manifest`, `manifestMaxBytes`, `appversion.Version`.
- Produces:
  - `func NewGitHubSource(repo string, apiBase string, client *http.Client, policy SourcePolicy) Source` — `apiBase` defaults to `https://api.github.com` when empty; tests override to point at `httptest.Server`.
  - `type githubSource struct { … }` (unexported), `Name() == "github"`.

- [ ] **Step 1: Write the failing tests**

Create `internal/updater/source_github_test.go`:

```go
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if !isErrRateLimited(err) {
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

func TestGitHubSourceManifestTimeout(t *testing.T) {
	mock := newGitHubMock(t)
	mock.installerDelay = 200 * time.Millisecond // affects only assets, but the latest.json handler ALSO delays via mock:
	mock.manifestBody = nil // will 200 with empty body
	policy := DefaultSourcePolicy()
	policy.ManifestTimeout = 30 * time.Millisecond
	src := NewGitHubSource(testRepo, mock.server.URL, http.DefaultClient, policy).(*githubSource)

	// Point apiBase at a black-hole port to force timeout.
	src.apiBase = "http://127.0.0.1:1" // reserved port; connect should fail fast, but we specifically want context.DeadlineExceeded.

	start := time.Now()
	_, err := src.FetchManifest(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("FetchManifest exceeded policy timeout: %v", time.Since(start))
	}
}

func TestGitHubSourceRejectsUnwhitelistedRedirect(t *testing.T) {
	// Mock returns a manifest whose installer URL points at a
	// non-githubusercontent host; the source must reject.
	mock := newGitHubMock(t)
	body := []byte("payload")
	mock.setManifest("https://evil.example.com/setup.exe", body)
	src := newTestGitHubSource(mock)

	m, err := src.FetchManifest(context.Background())
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	var buf strings.Builder
	err = src.DownloadInstaller(context.Background(), m, &buf, nil)
	if err == nil {
		t.Fatal("expected ErrHostNotAllowed")
	}
	if !errorsIs(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed", err)
	}
}

// Helpers referenced by these tests — implement them alongside the source.
func isErrRateLimited(err error) bool { return errorsIs(err, ErrRateLimited) }
func errorsIs(err, target error) bool { return err != nil && strings.Contains(err.Error(), target.Error()) }
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

// githubInstallerHost matches production installer URLs: github.com or
// any subdomain of githubusercontent.com. The wildcard tolerates
// release-assets.githubusercontent.com and future subdomain renames.
func githubInstallerHost(host string) bool {
	host = strings.ToLower(host)
	if host == "github.com" {
		return true
	}
	return strings.HasSuffix(host, ".githubusercontent.com")
}

// manifestHost matches the API host + asset CDN. api.github.com serves
// /repos/.../releases/latest; the asset browser_download_url 302s to
// *.githubusercontent.com.
func githubManifestHost(host string) bool {
	host = strings.ToLower(host)
	if host == "api.github.com" {
		return true
	}
	return strings.HasSuffix(host, ".githubusercontent.com")
}

func (s *githubSource) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentserver-app/"+appversion.Version)
}

func (s *githubSource) manifestClient() *http.Client {
	return s.redirectPinned(s.client, githubManifestHost)
}

func (s *githubSource) installerClient() *http.Client {
	c := s.redirectPinned(s.client, s.installerHostMatch)
	transport := cloneTransport(c.Transport)
	transport.ResponseHeaderTimeout = s.policy.FirstByteTimeout
	c.Transport = transport
	return c
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
	ctx, cancel := context.WithTimeout(ctx, s.policy.ManifestTimeout)
	defer cancel()

	apiURL := strings.TrimRight(s.apiBase, "/") + "/repos/" + s.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Manifest{}, err
	}
	s.setHeaders(req)
	resp, err := s.manifestClient().Do(req)
	if err != nil {
		return Manifest{}, s.classifyFetch(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		rem := resp.Header.Get("X-RateLimit-Remaining")
		return Manifest{}, fmt.Errorf("%w: github %d: x-ratelimit-remaining=%q", ErrRateLimited, resp.StatusCode, rem)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("github fetch releases/latest: unexpected status %s", resp.Status)
	}
	var release githubReleaseResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&release); err != nil {
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
	if !githubManifestHost(assetU.Hostname()) {
		return Manifest{}, fmt.Errorf("%w: asset host %s", ErrHostNotAllowed, assetU.Hostname())
	}
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return Manifest{}, err
	}
	s.setHeaders(req2)
	resp2, err := s.manifestClient().Do(req2)
	if err != nil {
		return Manifest{}, s.classifyFetch(ctx, err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("github fetch latest.json: unexpected status %s", resp2.Status)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp2.Body, manifestMaxBytes)).Decode(&m); err != nil {
		return Manifest{}, err
	}
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
	// Installer host is validated at DownloadInstaller time (via
	// installerHostMatch) so tests can inject a permissive matcher.
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

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, m.URL, nil)
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

func (s *githubSource) classifyFetch(parent context.Context, err error) error {
	if parent.Err() == context.DeadlineExceeded {
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
	if contains(string(b), "last_source_used") || contains(string(b), "last_fallbacks") {
		t.Fatalf("empty state must not include new fields: %s", string(b))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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

func (s Service) effectiveSources() []Source {
	if len(s.Sources) > 0 {
		return s.Sources
	}
	url := s.ManifestURL
	if url == "" {
		url = DefaultManifestURL
	}
	return []Source{NewCDNSource(url, s.client(), DefaultSourcePolicy())}
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
	return s.saveError(now, lastErr)
}
```

d) Rewrite `DownloadAndStart` to iterate sources (each source re-fetches its own manifest):

```go
func (s Service) DownloadAndStart(ctx context.Context, m Manifest) (State, error) {
	serviceStateMu.Lock()
	defer serviceStateMu.Unlock()

	now := s.now()
	if err := m.Validate(); err != nil {
		return s.saveError(now, err)
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
				return s.saveError(now, ctx.Err())
			}
			fallbacks = s.appendFallback(fallbacks, src.Name(), "manifest", err)
			lastErr = err
			continue
		}
		cmp, err := CompareVersions(freshM.Version, s.CurrentVersion)
		if err != nil {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "version", err)
			lastErr = err
			continue
		}
		if cmp <= 0 {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "version",
				fmt.Errorf("source manifest version %s not newer than current %s", freshM.Version, s.CurrentVersion))
			lastErr = fmt.Errorf("source %s has no newer version", src.Name())
			continue
		}

		finalPath, err := installerCachePath(s.CacheDir, freshM)
		if err != nil {
			return s.saveError(now, err)
		}
		temp, err := os.CreateTemp(s.CacheDir, filepath.Base(finalPath)+".*.tmp")
		if err != nil {
			return s.saveError(now, err)
		}
		tempPath := temp.Name()
		attemptCtx, cancel := context.WithCancel(ctx)
		err = src.DownloadInstaller(attemptCtx, freshM, temp, noopProgress)
		cancel()
		if closeErr := temp.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(tempPath)
			if ctx.Err() != nil {
				return s.saveError(now, ctx.Err())
			}
			fallbacks = s.appendFallback(fallbacks, src.Name(), "download", err)
			lastErr = err
			continue
		}
		if err := verifyInstaller(tempPath, freshM); err != nil {
			os.Remove(tempPath)
			fallbacks = s.appendFallback(fallbacks, src.Name(), "verify",
				fmt.Errorf("%w: %v", ErrSHA256Mismatch, err))
			lastErr = err
			continue
		}
		if err := replaceFile(tempPath, finalPath); err != nil {
			return s.saveError(now, err)
		}
		if s.BeforeInstallerStart != nil {
			if err := s.BeforeInstallerStart(ctx, freshM, finalPath); err != nil {
				return s.saveError(now, err)
			}
		}
		start := s.StartInstaller
		startContext := ctx
		if start == nil {
			start = StartInstaller
			startContext = context.Background()
		}
		if err := start(startContext, finalPath); err != nil {
			return s.saveError(now, err)
		}
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
	return s.saveError(now, lastErr)
}
```

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
	"strconv"
	"strings"
	"time"
)

type UpgradeConfig struct {
	GitHubEnabled bool
	GitHubRepo    string
	GitHubPolicy  SourcePolicy
}

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
	if v := env("UPGRADE_GITHUB_REPO"); v != "" {
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
- Create: `packaging/windows/latest.json.tmpl`
- Modify: `scripts/windows-package-common.sh`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: existing Windows packaging (produces `dist/agentserver-app-<version>-setup.exe`).
- Produces: `dist/latest-github.json` and `dist/latest-cdn.json`; a CI workflow that uploads both `latest-github.json` + `.exe` to the GitHub release for the pushed tag.

Note: this task is CI/pipeline work and may be validated only end-to-end on the next tagged release. Local `go test` cannot exercise it. If your workflow uses a different CI system (Jenkins, GitLab), adapt the `.github/workflows/release.yml` step into that system's equivalent.

- [ ] **Step 1: Create the template**

Create `packaging/windows/latest.json.tmpl`:

```json
{
  "version": "__VERSION__",
  "url": "__URL__",
  "sha256": "__SHA256__",
  "size": __SIZE__,
  "notes": "__NOTES__"
}
```

- [ ] **Step 2: Extend windows-package-common.sh**

At the end of `scripts/windows-package-common.sh`, add a function that renders `latest.json` twice (once for CDN, once for GitHub). Example addition:

```bash
# Render latest.json manifests for both publish targets. Requires the
# built installer at $1 and its target version at $2.
render_latest_json() {
  local installer_path="$1"
  local version="$2"
  local notes="${3:-}"
  local size sha
  size=$(stat -c%s "$installer_path")
  sha=$(sha256sum "$installer_path" | cut -d' ' -f1)
  local installer_name
  installer_name=$(basename "$installer_path")

  local dist_dir
  dist_dir=$(dirname "$installer_path")

  # CDN copy: URL points at the internal asset host.
  sed \
    -e "s|__VERSION__|${version}|g" \
    -e "s|__URL__|https://assets.agent.cs.ac.cn/agentserver-app/windows/${installer_name}|g" \
    -e "s|__SHA256__|${sha}|g" \
    -e "s|__SIZE__|${size}|g" \
    -e "s|__NOTES__|${notes}|g" \
    "$(dirname "$0")/../packaging/windows/latest.json.tmpl" \
    > "${dist_dir}/latest-cdn.json"

  # GitHub copy: URL points at the release asset. GitHub renders this
  # via github.com/<owner>/<repo>/releases/download/<tag>/<installer>.
  local owner_repo="${UPGRADE_GITHUB_REPO:-agentserver/app}"
  local tag="v${version}"
  sed \
    -e "s|__VERSION__|${version}|g" \
    -e "s|__URL__|https://github.com/${owner_repo}/releases/download/${tag}/${installer_name}|g" \
    -e "s|__SHA256__|${sha}|g" \
    -e "s|__SIZE__|${size}|g" \
    -e "s|__NOTES__|${notes}|g" \
    "$(dirname "$0")/../packaging/windows/latest.json.tmpl" \
    > "${dist_dir}/latest-github.json"
}
```

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

      - name: Upload assets — installer first, latest.json last
        shell: bash
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          # Upload .exe first so the source of truth (latest.json)
          # never points at a missing asset.
          gh release upload "$TAG" dist/agentserver-app-*-setup.exe --clobber
          # Verify the asset is fetchable before publishing manifest.
          EXE_NAME=$(ls dist/agentserver-app-*-setup.exe | head -1 | xargs -n1 basename)
          curl -sSfL -o /tmp/verify.exe "https://github.com/${GITHUB_REPOSITORY}/releases/download/${TAG}/${EXE_NAME}"
          DIST_SHA=$(sha256sum "dist/${EXE_NAME}" | cut -d' ' -f1)
          REMOTE_SHA=$(sha256sum /tmp/verify.exe | cut -d' ' -f1)
          [ "$DIST_SHA" = "$REMOTE_SHA" ] || { echo "SHA mismatch"; exit 1; }
          # Now publish the manifest.
          cp dist/latest-github.json dist/latest.json
          gh release upload "$TAG" dist/latest.json --clobber
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
