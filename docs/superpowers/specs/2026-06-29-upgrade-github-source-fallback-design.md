# Upgrade: GitHub Release Source with CDN Fallback

**Date:** 2026-06-29
**Status:** Draft / awaiting user review
**Owner:** Zishu Yu

## Background

The launcher's auto-updater (`internal/updater`) currently fetches a single
manifest from a hardcoded internal CDN
(`https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json`) and
downloads the .exe installer from the same host, every 24h. There is no
GitHub Releases integration today. We want updates to prefer the public
GitHub Releases of `agentserver/app` when reachable, and fall back to the
existing CDN path on any timeout, rate-limit, or sustained low download
speed, with no behavioral regression in CDN-only mode.

The recent v0.0.8 release adds the multi-model Loom catalog; nothing about
the upgrade pipeline shape has changed for that work, and this design does
not touch loom installation or the post-install daemon path.

## Goals

1. Try GitHub Releases first when the GitHub source is enabled, fall back
   to the internal CDN on failure.
2. Detect "too slow" mid-download and switch sources without waiting for
   socket timeout.
3. Keep the existing CDN behavior 100% intact when the GitHub source is
   disabled (default).
4. Surface which source was used and why fallback happened, in
   `update-state.json` and the console HTTP API.
5. Make the source set extensible: adding a third source (e.g. an R2
   mirror) should mean implementing one interface, not editing scheduler
   logic.

## Non-Goals

- Resuming a CDN download from where a GitHub download stalled. Cancelled
  GitHub partial is discarded; CDN downloads from byte 0.
- Auth tokens for GitHub. Repo is public; anonymous queries only. Hitting
  the 60/h anonymous rate limit is treated as failure → fallback.
- A user-facing "try GitHub first" toggle in the UI. Source order is a
  process-level config, not a per-update choice.
- Changing the installer signature / SHA256 verification — both sources
  must publish identical bytes (same release asset, same SHA256).
- Reworking `internal/download/resumable.go`. Speed monitoring for this
  feature is implemented inside the source layer; the resumable package
  is left untouched.

## Architecture

```
internal/updater/
├── service.go             (modified)  Orchestrator; owns []Source, runs fallback
├── manifest.go            (modified)  allowedManifestHost → per-source whitelist
├── state.go               (modified)  Add LastSourceUsed, LastFallbacks
├── source.go              (new)       Source interface + SpeedSample + sentinels
├── source_cdn.go          (new)       Existing CDN behavior, moved behind Source
├── source_github.go       (new)       GitHub release: fetch latest.json asset
├── speed_monitor.go       (new)       Sliding-window speed measurement
├── source_cdn_test.go     (new)
├── source_github_test.go  (new)
├── speed_monitor_test.go  (new)
└── service_fallback_test.go (new)    Fake-source-driven scheduler tests
```

### Source interface

```go
package updater

// SpeedSample is reported during DownloadInstaller via the onProgress
// callback. The source's speed monitor produces these; the scheduler
// does not interpret them — they exist so tests can observe the wire.
type SpeedSample struct {
    Downloaded  int64
    Elapsed     time.Duration
    BytesPerSec float64
}

// Source is a single upgrade origin (GitHub release, internal CDN, …).
// Implementations are responsible for their own host whitelisting,
// timeouts, and slow-download detection — they signal back to the
// scheduler by returning an error wrapping ErrSlowDownload or
// ErrFetchTimeout. The scheduler treats any non-nil error as a reason
// to try the next source.
type Source interface {
    Name() string
    FetchManifest(ctx context.Context) (Manifest, error)
    DownloadInstaller(
        ctx context.Context,
        m Manifest,
        dst string,
        onProgress func(SpeedSample),
    ) error
}

// Sentinel errors. Sources wrap these when fallback should be recorded
// with a structured reason in state.LastFallbacks.
var (
    ErrFetchTimeout    = errors.New("source: fetch timeout")
    ErrSlowDownload    = errors.New("source: download too slow")
    ErrRateLimited     = errors.New("source: rate limited")
    ErrHostNotAllowed  = errors.New("source: host not in allowlist")
)
```

### Scheduler in Service

`Service.Check()` and `Service.DownloadAndStart()` collapse into a single
flow that iterates `s.sources` in order:

```go
func (s *Service) checkAndDownload(parent context.Context) (Result, error) {
    var fallbacks []FallbackRecord
    for _, src := range s.sources {
        ctx, cancel := context.WithCancel(parent)
        m, err := src.FetchManifest(ctx)
        if err != nil {
            cancel()
            fallbacks = append(fallbacks, record(src, "manifest", err))
            continue
        }
        dst := s.stagingPath(m)
        err = src.DownloadInstaller(ctx, m, dst, s.onProgress)
        cancel()
        if err != nil {
            _ = os.Remove(dst)
            fallbacks = append(fallbacks, record(src, "download", err))
            continue
        }
        s.recordSuccess(src.Name(), fallbacks)
        return Result{Manifest: m, Path: dst}, nil
    }
    return Result{}, fmt.Errorf("all sources failed: %v", fallbacks)
}
```

Key constraints:
- One `ctx, cancel` per source attempt. Speed monitor inside the source
  calls `cancel()` when its window fires; the source's I/O loop then
  exits with `ctx.Err()` and the source wraps that into `ErrSlowDownload`.
- Partial downloads at `dst` are removed before the next source tries —
  no chance of stale-bytes confusion at SHA256 verify time.
- `s.sources` is set at `Service` construction. Default builder returns
  `{cdnSource}`. When `UPGRADE_GITHUB_REPO` env or config has
  `github.enabled=true` + a non-empty `repo`, the builder returns
  `{githubSource, cdnSource}`. **No env/config change ⇒ identical to
  today.**

## Source policy (timeouts and speed thresholds)

```go
type SourcePolicy struct {
    ManifestTimeout     time.Duration // default 5s
    FirstByteTimeout    time.Duration // default 10s
    MinSpeedBytesPerSec int64         // default 100 * 1024 (100 KB/s)
    SpeedWindow         time.Duration // default 10s
}
```

- Each `Source` impl holds its own `SourcePolicy`. GitHub and CDN may
  diverge later (cross-border GitHub probably warrants different
  numbers than a domestic CDN), but both start at the same defaults.
- `ManifestTimeout` is the per-request timeout on the manifest HTTP call.
- `FirstByteTimeout` is enforced via `http.Transport.ResponseHeaderTimeout`
  — covers slow DNS, TLS, and idle server.
- `MinSpeedBytesPerSec` + `SpeedWindow`: once `SpeedWindow` seconds of
  download have elapsed, the trailing-`SpeedWindow` average is computed
  every second; if it drops below `MinSpeedBytesPerSec`, `cancel()`.
- Policy fields are read from `internal/slave/config.go` (path TBD in
  plan phase) with the defaults above; ops can override per source.

### Speed monitor mechanics

`speed_monitor.go` exposes:

```go
type speedMonitor struct {
    window      time.Duration
    minBPS      int64
    samples     []sample          // ring buffer, 1 entry/second
    cancel      context.CancelFunc
    onSample    func(SpeedSample)
}

// Wraps the response body so every Read updates the byte counter.
func (m *speedMonitor) wrap(r io.Reader) io.Reader { ... }

// Started by the Source after Read begins; runs until ctx done.
func (m *speedMonitor) run(ctx context.Context) { ... }
```

`run` ticks once per second, samples `(now, bytesSoFar)`, computes the
average over the trailing window, fires `onSample`, and on threshold
breach calls `cancel()`. It does **not** fire before the window is full,
so transient slow starts (TLS handshake, CDN warm-up) don't trip it.

## GitHub source details

- Manifest endpoint:
  `https://api.github.com/repos/agentserver/app/releases/latest`
  Parse the JSON, find the asset with `name == "latest.json"`, GET its
  `browser_download_url` (≤ 64 KB, same `manifestMaxBytes` as today),
  unmarshal into the existing `Manifest` struct.
- Repo is hardcoded to `agentserver/app` as the **default**; override
  via `UPGRADE_GITHUB_REPO=owner/repo` env or config.
- Host whitelists for the GitHub source:
  - Manifest fetch (release API + asset URL):
    `api.github.com`, `objects.githubusercontent.com`,
    `*.githubusercontent.com`
  - Installer URL (inside `latest.json`):
    `github.com`, `objects.githubusercontent.com`,
    `*.githubusercontent.com`
- HTTP 403 / 429 / `x-ratelimit-remaining: 0` → wrap `ErrRateLimited`.
- Redirect handling: `http.Client.CheckRedirect` validates each hop
  against the installer whitelist (GitHub release downloads 302 to
  `objects.githubusercontent.com`, which we expect).
- No retries inside the source. One miss ⇒ fall through to CDN; the
  24h scheduler will retry the whole flow naturally.

## CDN source details

- Verbatim port of today's `fetchManifest` + download loop into
  `source_cdn.go`.
- Whitelist remains `assets.agent.cs.ac.cn` exactly, moved from the
  `allowedManifestHost` package constant in `manifest.go` to a
  `cdnSource` field.
- When `s.sources == [cdnSource]` (GitHub disabled), observable
  behavior is identical: same manifest URL, same SHA256 verification,
  same state.json fields except for the new optional ones (which are
  empty strings / empty slices).

## State and observability

`internal/updater/state.go` `State` adds:

```go
type FallbackRecord struct {
    Source string `json:"source"` // "github" / "cdn"
    Stage  string `json:"stage"`  // "manifest" / "download"
    Reason string `json:"reason"` // sentinel error name or wrapped msg
    Tried  string `json:"tried"`  // RFC3339 timestamp
}

type State struct {
    // existing fields …
    LastSourceUsed string           `json:"last_source_used,omitempty"`
    LastFallbacks  []FallbackRecord `json:"last_fallbacks,omitempty"`
}
```

- `LastFallbacks` is capped to the most recent 5 entries.
- `internal/console/update.go` exposes both fields verbatim in its API
  response so the frontend / ops can show "this update came via CDN
  because GitHub timed out after 5s on the manifest fetch."

## Error handling and cancellation

- The outer `parent` context comes from the existing 24h scheduling
  loop or a user-triggered check. The scheduler derives a per-source
  `(ctx, cancel)`.
- Source returns:
  - `nil` ⇒ success, scheduler stops.
  - any `error` ⇒ scheduler records `FallbackRecord` and tries next.
- `parent.Done()` (user closed launcher / `Service.Stop()`) ⇒ scheduler
  returns the parent's `ctx.Err()` immediately without consulting
  remaining sources.
- Partial files at `dst` are removed before each next-source attempt
  and after the final failure.

## Configuration

New keys (defaults shown):

```
upgrade:
  sources: [github, cdn]      # order; empty = cdn-only legacy
  github:
    enabled: false            # gates inclusion of github source
    repo: agentserver/app
    policy:
      manifest_timeout: 5s
      first_byte_timeout: 10s
      min_speed_bps: 102400   # 100 KB/s
      speed_window: 10s
  cdn:
    manifest_url: https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json
    allowed_host: assets.agent.cs.ac.cn
    policy:
      manifest_timeout: 5s
      first_byte_timeout: 10s
      min_speed_bps: 102400
      speed_window: 10s
```

Environment overrides (highest priority):
- `UPGRADE_GITHUB_ENABLED=true|false`
- `UPGRADE_GITHUB_REPO=owner/repo`

When `upgrade.github.enabled == false` (default), the builder skips the
github source and the resulting `[]Source` is `{cdnSource}` — full
backward compatibility.

## Testing strategy (TDD order)

Write tests first, in this order:

1. **`speed_monitor_test.go`** — drive the monitor with a fake clock
   and a fake `cancel`, assert:
   - Below-threshold within the first window ⇒ no cancel.
   - Window full, trailing avg below threshold ⇒ cancel within 1s.
   - Window full, trailing avg above threshold ⇒ no cancel.
   - `onSample` fires once per tick.

2. **`source_github_test.go`** — `httptest.Server` simulating the
   GitHub API + asset redirect:
   - Happy path: returns valid `latest.json`, asset 302 → secondary
     server, file downloads, SHA256 OK.
   - Manifest 403 with `x-ratelimit-remaining: 0` ⇒ `ErrRateLimited`.
   - Manifest response > 64 KB ⇒ error.
   - Redirect to non-whitelisted host ⇒ `ErrHostNotAllowed`.
   - Manifest hangs > `ManifestTimeout` ⇒ `ErrFetchTimeout`.
   - Download throttled below `MinSpeedBytesPerSec` for a full window
     ⇒ `ErrSlowDownload`.

3. **`source_cdn_test.go`** — port today's CDN-side updater test
   into the new shape; assert identical behavior.

4. **`service_fallback_test.go`** — inject two fake sources controllable
   per-method:
   - Both healthy ⇒ first source wins, `LastSourceUsed="github"`,
     `LastFallbacks` empty.
   - First manifest times out ⇒ second wins, `LastFallbacks` has one
     entry `(github, manifest, timeout)`.
   - First download first-byte times out ⇒ same, stage="download".
   - First download starts then goes slow ⇒ same.
   - Both fail ⇒ `checkAndDownload` returns error; `LastFallbacks` has
     two entries; partial file removed.
   - Parent ctx cancelled mid-flight ⇒ no fallback, returns `ctx.Err()`.

All existing `internal/updater` tests must continue to pass unchanged.

## Migration / rollout

- Default config keeps `github.enabled=false`, so day-1 behavior is
  identical to today.
- After v0.0.9 publishes `latest.json` as a release asset on
  `agentserver/app`, ops flips `github.enabled=true` (or sets
  `UPGRADE_GITHUB_ENABLED=true`) on a canary; the next 24h tick uses
  GitHub first, CDN as fallback.
- After validation across regions, flip the default to `true` in a
  later release.

## Risks

- **Anonymous GitHub rate limit (60/h/IP)** in NATted environments
  where many launchers share an egress IP. Mitigated by 24h cadence
  per launcher + automatic CDN fallback on 403. Worst case: GitHub
  source is effectively unused in that environment; CDN still works.
- **GitHub asset CDN slow in some regions.** This is the entire point
  of the speed-window fallback; the threshold default (100 KB/s avg
  over 10s) is intentionally lenient so we don't ping-pong sources on
  every small slowdown.
- **Identical SHA256 across sources** is a publisher discipline issue,
  not a code issue. Verification will fail if they diverge, which
  fails the source and falls back — correct behavior, but worth
  documenting in the release runbook.
