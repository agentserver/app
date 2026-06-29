# Upgrade: GitHub Release Source with CDN Fallback

**Date:** 2026-06-29
**Status:** Draft v2 (post review-round-1)
**Owner:** Zishu Yu

## Background

The launcher's auto-updater (`internal/updater`) currently fetches a single
manifest from a hardcoded internal CDN
(`https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json`) and
downloads the .exe installer from the same host. There is no GitHub
Releases integration today. Each launcher process runs a polling loop
in `cmd/launcher/main.go::scheduleAutomaticUpdateCheckWithRetry` (the
"24h" cadence comes from `AutoCheckEvery`, which is a *skip threshold*
inside `Service.Check`, not a ticker — the actual ticker lives in
`cmd/launcher`).

The user-facing flow is two-step and stays two-step in this design:

1. `console.Controller.CheckUpdate` → `Service.Check` → returns a manifest
   so the UI can show "v0.0.9 available, do you want to install?"
2. User clicks → `console.Controller.InstallUpdate` → `Service.DownloadAndStart`.

We want updates to prefer the public GitHub Releases of `agentserver/app`
when reachable, and fall back to the existing CDN path on any timeout,
rate-limit, or sustained low download speed, with no behavioral
regression in CDN-only mode (which remains the default).

## Goals

1. Try GitHub Releases first when the GitHub source is enabled, fall back
   to the internal CDN on failure.
2. Detect "too slow" mid-download and switch sources without waiting for
   the socket timeout.
3. Keep existing CDN behavior 100% intact when the GitHub source is
   disabled (default).
4. Surface which source was used and why fallback happened, in
   `update-state.json` and the console HTTP API.
5. Make the source set extensible: adding a third source (e.g. an R2
   mirror) should mean implementing one interface, not editing scheduler
   logic.
6. Publish `latest.json` to GitHub release as part of this change so the
   GitHub source has something to fetch on day 1.

## Non-Goals

- Collapsing the two-step Check / Install UI flow into a single call.
  Both `Check` and `DownloadAndStart` are extended to iterate sources
  independently; the public API surface of `Service` is preserved.
- Resuming a CDN download from where a GitHub download stalled. The
  cancelled GitHub partial is discarded; CDN downloads from byte 0.
- Auth tokens for GitHub. The repo is public; anonymous queries only.
  Anonymous 60/h IP rate limit is treated as failure → fallback.
- A user-facing "try GitHub first" toggle in the UI. Source order is a
  process-level config, not a per-update choice.
- Introducing a dependency on `internal/download/resumable.go`. The
  updater does not import that package today and won't after this
  change; speed monitoring is implemented locally in
  `internal/updater/speed_monitor.go`.
- Plumbing a download-progress callback up to the console / frontend.
  `SpeedSample` exists for source-internal monitor logic and tests
  today; piping it to a SSE / progress UI is a follow-up.

## Architecture

```
internal/updater/
├── service.go             (modified)  Hosts Sources []Source; Check & DownloadAndStart iterate
├── manifest.go            (modified)  Remove host check from Validate (move to source)
├── state.go               (modified)  Add LastSourceUsed, LastFallbacks
├── config.go              (new)       UpgradeConfig + BuildSources
├── source.go              (new)       Source interface, SpeedSample, sentinels
├── source_cdn.go          (new)       Today's CDN behavior, behind Source
├── source_github.go       (new)       GitHub release: fetch latest.json asset
├── speed_monitor.go       (new)       Sliding-window speed measurement + clock injection
├── source_cdn_test.go     (new)       Inherits host-allowlist tests migrated from manifest_test.go
├── source_github_test.go  (new)
├── speed_monitor_test.go  (new)
└── service_fallback_test.go (new)    Fake-source scheduler tests

cmd/launcher/main.go       (modified)  newCompletedUpdater reads env, constructs Sources

packaging/                 (modified)  Release publishing now uploads latest.json as a GitHub release asset
```

### Source interface

```go
package updater

// SpeedSample is reported during DownloadInstaller via the onProgress
// callback. The source's speed monitor produces these; the scheduler
// only forwards them. They exist for tests today and for a future
// progress-UI hookup; the scheduler does not interpret them.
type SpeedSample struct {
    Downloaded  int64
    Elapsed     time.Duration
    BytesPerSec float64
}

// Source is a single upgrade origin (GitHub release, internal CDN, …).
// Implementations are responsible for their own host whitelisting,
// timeouts, and slow-download detection. They signal back to the
// scheduler by returning an error wrapping one of the sentinels below.
// The scheduler treats any non-nil error as a reason to try the next
// source.
type Source interface {
    Name() string
    FetchManifest(ctx context.Context) (Manifest, error)
    // DownloadInstaller writes installer bytes to dst. SHA256
    // verification is NOT the source's responsibility — the scheduler
    // verifies after this returns nil. A source whose I/O loop sees
    // ctx.Canceled MUST inspect its internal slow-download flag and
    // wrap ErrSlowDownload when applicable; otherwise return ctx.Err()
    // directly.
    DownloadInstaller(
        ctx context.Context,
        m Manifest,
        dst io.Writer,
        onProgress func(SpeedSample),
    ) error
}

// Sentinel errors. Sources wrap these; scheduler records the wrapped
// kind in FallbackRecord.Reason so /update-state can show why fallback
// happened.
var (
    ErrFetchTimeout    = errors.New("source: fetch timeout")
    ErrSlowDownload    = errors.New("source: download too slow")
    ErrRateLimited     = errors.New("source: rate limited")        // any 403/429
    ErrHostNotAllowed  = errors.New("source: host not in allowlist")
    ErrSHA256Mismatch  = errors.New("source: installer sha256 mismatch")
)
```

### Scheduler in Service

Both `Check` and `DownloadAndStart` keep their existing signatures,
return values, and observable state transitions. Inside, they each
loop over `s.Sources`:

```go
// Check, after the AutoCheckEvery skip-threshold gate (unchanged):
var fallbacks []FallbackRecord
for _, src := range s.Sources {
    ctx, cancel := context.WithCancel(parent)
    m, err := src.FetchManifest(ctx)
    cancel()
    if err != nil {
        fallbacks = append(fallbacks, s.record(src.Name(), "manifest", err))
        continue
    }
    return s.saveAvailable(now, m, src.Name(), fallbacks)
}
return s.saveError(now, fmt.Errorf("all sources failed: %v", fallbacks))

// DownloadAndStart iterates the same way but in the download stage:
for _, src := range s.Sources {
    ctx, cancel := context.WithCancel(parent)
    if err := src.DownloadInstaller(ctx, m, temp, noopProgress); err != nil {
        cancel(); s.discardTemp(tempPath)
        fallbacks = append(fallbacks, s.record(src.Name(), "download", err))
        continue
    }
    cancel()
    if err := verifyInstaller(tempPath, m); err != nil {                  // (#7) verify in scheduler
        s.discardTemp(tempPath)
        fallbacks = append(fallbacks, s.record(src.Name(), "verify",
            fmt.Errorf("%w: %v", ErrSHA256Mismatch, err)))
        continue
    }
    // … existing replaceFile / BeforeInstallerStart / StartInstaller path …
    return s.saveInstallerStarted(now, m, src.Name(), fallbacks)
}
return s.saveError(now, fmt.Errorf("all sources failed: %v", fallbacks))
```

Key constraints:
- One `ctx, cancel` per source attempt. The source's speed monitor
  calls `cancel()` when its window fires; the source's I/O loop exits
  with `ctx.Err()` and the source wraps it into `ErrSlowDownload`
  based on the monitor's `tripped` flag.
- Partial files at `tempPath` are removed before the next source
  tries — no chance of stale-bytes confusion at verify time.
- SHA256 verification stays in the scheduler (today's `verifyInstaller`
  in `service.go`); both sources must publish identical bytes for the
  same release. A mismatch is recorded as a fallback reason and the
  next source is tried.
- `s.Sources` is set at `Service` construction. Default (no env / no
  config) is `{cdnSource}`; with GitHub enabled it becomes
  `{githubSource, cdnSource}`. **No env change ⇒ identical to today.**

## Source policy (timeouts and speed thresholds)

```go
type SourcePolicy struct {
    ManifestTimeout     time.Duration // default 5s
    FirstByteTimeout    time.Duration // default 10s
    MinSpeedBytesPerSec int64         // default 100 * 1024 (100 KB/s)
    SpeedWindow         time.Duration // default 10s
}
```

- Each `Source` impl holds its own `SourcePolicy` and its own
  `*http.Client` constructed in the builder. The two sources do not
  share clients (different `ResponseHeaderTimeout`, different
  `CheckRedirect` whitelists).
- `ManifestTimeout` is the per-request deadline on the manifest call,
  applied via `context.WithTimeout` inside `FetchManifest`.
- `FirstByteTimeout` is enforced via
  `http.Transport.ResponseHeaderTimeout` on the installer client.
- `MinSpeedBytesPerSec` + `SpeedWindow`: once `SpeedWindow` seconds
  have elapsed since download start, the trailing-`SpeedWindow`
  average is computed every tick; if it drops below
  `MinSpeedBytesPerSec`, the monitor sets `tripped=true` and calls
  `cancel()`. Before the window fills, the monitor does nothing.

### Speed monitor mechanics

```go
type speedMonitor struct {
    window   time.Duration
    minBPS   int64
    now      func() time.Time          // (#12) injected; production = time.Now
    tick     <-chan time.Time          // (#12) injected; production = time.Tick(1s)
    samples  []sample                  // ring buffer, 1/tick
    bytes    atomic.Int64
    cancel   context.CancelFunc
    onSample func(SpeedSample)
    tripped  atomic.Bool               // (#13) source reads this on ctx.Err()
}

func (m *speedMonitor) wrap(r io.Reader) io.Reader   // updates m.bytes per Read
func (m *speedMonitor) run(ctx context.Context)       // ticks until ctx.Done
func (m *speedMonitor) Tripped() bool                 // post-run inspection
```

The injected `tick` channel lets tests drive the monitor with a
hand-controlled `chan time.Time` and a fake `now`, eliminating the
real-time flakiness the original "cancel within 1s" assertion would
have caused.

## GitHub source details

- **Manifest endpoint:**
  `https://api.github.com/repos/<repo>/releases/latest`
  where `<repo>` defaults to `agentserver/app` (verified against
  `git remote -v`) and can be overridden via `UPGRADE_GITHUB_REPO`.
  Parse the JSON, find the asset with `name == "latest.json"`, GET
  its `browser_download_url` (≤ `manifestMaxBytes`, reusing the
  existing constant in `service.go`), unmarshal into the existing
  `Manifest` struct.

- **Manifest URL field semantics:** the `url` field in the GitHub
  copy of `latest.json` MUST point at a GitHub-hosted location
  (host matches the installer whitelist below). The CDN copy of
  `latest.json` continues to point at `assets.agent.cs.ac.cn`. Both
  copies share the same `version`, `sha256`, `size`, `notes`. The
  release pipeline produces both files from one source of truth.

- **Host whitelists (GitHub source only):**
  - Manifest fetch hosts: `api.github.com` and any subdomain of
    `githubusercontent.com` (handles the 302 from the asset
    `browser_download_url`).
  - Installer URL host (inside `latest.json`): any subdomain of
    `githubusercontent.com` (`release-assets.githubusercontent.com`
    is today's hop; the wildcard tolerates GitHub renaming) plus
    `github.com`.
  - Implemented as a small "host matcher" function inside
    `source_github.go`; CDN source has its own matcher pinned to
    `AssetsHost`.

- **HTTP 403 / 429:** both wrap `ErrRateLimited` (secondary rate
  limit, abuse detection, and primary rate limit can each show up as
  403 with different headers; the scheduler treats them identically —
  fall back). The reason string in `FallbackRecord.Reason` records
  finer detail (`"github 403: x-ratelimit-remaining=0"` etc.) for
  ops visibility.

- **Redirects:** `http.Client.CheckRedirect` validates each hop
  against the installer whitelist. Unexpected hosts wrap
  `ErrHostNotAllowed`.

- **No internal retries.** One miss ⇒ fall through to CDN; the
  launcher's existing scheduler retries the whole flow on its own
  cadence.

## CDN source details

- Verbatim port of today's `fetchManifest` + download loop into
  `source_cdn.go`. Same `*http.Client` pinning, same `manifestMaxBytes`
  cap, same `io.LimitReader(resp.Body, m.Size+1)` size guard.
- Whitelist is `AssetsHost` (existing constant from `manifest.go`),
  consulted inside the CDN source's own redirect validator.
- When `s.Sources == [cdnSource]` (GitHub disabled, the default),
  observable behavior is identical to today: same manifest URL, same
  SHA256 verification, same `update-state.json` fields except the new
  optional `last_source_used: "cdn"` and (empty) `last_fallbacks`.

## Manifest.Validate() — chosen split (option a)

`Manifest.Validate()` is reduced to format-only checks: version parse,
SHA256 format, size positive, URL has https scheme + non-empty host.
**Host allowlist is removed from `Validate()`** and moved into each
source's redirect / response check.

**This breaks the promise that "all existing tests pass unchanged."**
Concretely:

- `manifest_test.go` tests covering the AssetsHost allowlist
  (`TestManifestValidateRejectsURLOutsideAssetsHost`,
  `TestManifestValidateRejectsAssetsHostSuffixBypass`,
  `TestManifestValidateRejectsAssetsHostUserinfoBypass`,
  `TestManifestValidateAcceptsMixedCaseAssetsHost`) move to
  `source_cdn_test.go` and are re-expressed against the CDN source's
  validator. The assertions stay equivalent; only the unit-under-test
  changes.
- `service_test.go` exercises the redirect-pinned client via
  `serviceTestRoundTripper`; it must continue to pass because the CDN
  source preserves the same redirect-validation behavior.
- The source-level lint tests in `service_source_test.go` and
  `installer_windows_source_test.go` are reviewed manually for
  substring assertions that might no longer hold after the move
  (e.g. `start = StartInstaller` and `startContext = context.Background()`
  remain). Both are kept intact in the new code path.

## State and observability

`internal/updater/state.go` `State` adds:

```go
type FallbackRecord struct {
    Source string    `json:"source"`           // "github" / "cdn"
    Stage  string    `json:"stage"`            // "manifest" / "download" / "verify"
    Reason string    `json:"reason"`           // wrapped sentinel + free-form detail
    Tried  time.Time `json:"tried"`            // populated via s.Now() (#17), not time.Now()
}

type State struct {
    // existing fields …
    LastSourceUsed string           `json:"last_source_used,omitempty"`
    LastFallbacks  []FallbackRecord `json:"last_fallbacks,omitempty"`
}
```

- **`LastFallbacks` semantics:** a rolling history across *all* recent
  attempts (not just the current attempt). Capped to the 5 most recent
  entries. A successful attempt that previously fell back still
  preserves the prior failures in the buffer, so ops can see "the last
  3 GitHub attempts went to CDN because of rate limiting" days later.
- `LastSourceUsed` is set from the source that ultimately succeeded
  in the most recent `Check` or `DownloadAndStart`. Empty until a
  source succeeds.
- `internal/console/update.go` exposes both fields verbatim through
  the existing `/update-state` payload — JSON tags do the work, no
  controller change needed unless we want to add a typed wrapper.

## Error handling and cancellation

- The `parent` context is the caller's context (24h scheduler in
  `cmd/launcher`, or a console request). The scheduler derives a
  per-source `(ctx, cancel)`.
- Source return values:
  - `nil` ⇒ source succeeded; scheduler proceeds (download → verify).
  - non-`nil` ⇒ scheduler records `FallbackRecord` and tries the next
    source.
- `parent.Done()` (process shutting down, console request cancelled)
  ⇒ scheduler stops iterating and returns `parent.Err()` immediately.
- Partial files at `tempPath` are removed before each next-source
  attempt and after the final failure.

## Configuration (env-first, YAML-free for now)

The existing `internal/slave/config.go` is write-only (serialises a
struct to YAML for loom). There is no Viper / config-loader in the
process. Rather than introduce one for this feature, configuration is
**environment-variable only**:

```
UPGRADE_GITHUB_ENABLED=true|false        # default false
UPGRADE_GITHUB_REPO=owner/repo           # default agentserver/app
UPGRADE_GITHUB_MANIFEST_TIMEOUT=5s       # optional
UPGRADE_GITHUB_FIRST_BYTE_TIMEOUT=10s    # optional
UPGRADE_GITHUB_MIN_SPEED_BPS=102400      # optional
UPGRADE_GITHUB_SPEED_WINDOW=10s          # optional

UPGRADE_CDN_MANIFEST_URL=...             # optional, default existing constant
UPGRADE_CDN_MANIFEST_TIMEOUT=5s          # optional
UPGRADE_CDN_FIRST_BYTE_TIMEOUT=10s       # optional
UPGRADE_CDN_MIN_SPEED_BPS=102400         # optional
UPGRADE_CDN_SPEED_WINDOW=10s             # optional
```

Reading happens in a new `internal/updater/config.go`:

```go
type UpgradeConfig struct {
    GitHubEnabled bool
    GitHubRepo    string
    GitHubPolicy  SourcePolicy
    CDNManifestURL string
    CDNPolicy      SourcePolicy
}

func LoadUpgradeConfig(env func(string) string) UpgradeConfig { ... }
func BuildSources(cfg UpgradeConfig, http httpClientFactory) []Source { ... }
```

`cmd/launcher/main.go::newCompletedUpdater` (line 342 today) becomes:

```go
cfg := updater.LoadUpgradeConfig(os.Getenv)
return &updater.Service{
    CurrentVersion: appversion.Version,
    CacheDir:       p.UpdatesCacheDir,
    State:          updater.NewStateStore(p.UpdateStateFile),
    Sources:        updater.BuildSources(cfg, updater.DefaultHTTPFactory),
}
```

`ManifestURL` on `Service` becomes legacy / removed — its role moves
into the CDN source's own field. Existing test fixtures that set
`ManifestURL` on a bare `Service` literal need migrating to set
`Sources` via a test helper (`updater.WithCDNSourceURL(t, url)`),
which the plan phase will enumerate.

If we later add YAML config, this same `UpgradeConfig` struct is what
gets populated from it; no API redesign needed.

## Release-pipeline change (required, not a Day-N flag flip)

For the GitHub source to be useful immediately when ops sets
`UPGRADE_GITHUB_ENABLED=true`, the publishing pipeline must upload
`latest.json` as a GitHub release asset alongside the .exe. This
work is **in scope for this change**:

- `packaging/windows/installer.iss` (or the CI workflow that consumes
  it) gains a step that:
  1. Computes the SHA256 of the built `.exe`.
  2. Renders a `latest.json` with `version`, GitHub asset
     `browser_download_url`, `sha256`, `size`, `notes`.
  3. Uploads both files (`latest.json` + `.exe`) to the GitHub release
     for the tag.
- The same content (but with the CDN URL in the `url` field) is
  uploaded to `assets.agent.cs.ac.cn` as today.
- Plan phase will identify whether this lives in an existing CI
  workflow file or `packaging/windows/*.sh`; today's repo has
  `scripts/windows-package-common.sh` as a strong candidate.

Without this pipeline change, enabling the GitHub source results in a
404 on the asset and immediate CDN fallback — safe but pointless.

## Testing strategy (TDD order)

Write tests first, in this order:

1. **`speed_monitor_test.go`** — drive the monitor with a fake clock
   (injected `now`) and a fake `tick` channel:
   - No samples before window full ⇒ no cancel.
   - Window full, trailing avg below threshold ⇒ `cancel()` called and
     `Tripped()` reports true on the next read.
   - Window full, trailing avg above threshold ⇒ no cancel, `Tripped()`
     false.
   - `onSample` fires once per tick.

2. **`source_github_test.go`** — `httptest.Server` simulating the
   GitHub API and the asset redirect chain:
   - Happy path: returns valid `latest.json`, asset 302 →
     `release-assets.githubusercontent.com` mock, body downloads.
   - Manifest 403 (with and without `x-ratelimit-remaining: 0`) ⇒
     `ErrRateLimited`.
   - Manifest 429 ⇒ `ErrRateLimited`.
   - Manifest response > `manifestMaxBytes` ⇒ error.
   - Redirect to non-whitelisted host ⇒ `ErrHostNotAllowed`.
   - Manifest exceeds `ManifestTimeout` ⇒ `ErrFetchTimeout`.
   - Download throttled below `MinSpeedBytesPerSec` for a full
     window ⇒ `ErrSlowDownload`.
   - Mismatched-bytes case: source returns nil, scheduler-level test
     (#4 below) covers `ErrSHA256Mismatch`.

3. **`source_cdn_test.go`** — port the CDN-side updater tests
   (including the migrated host-allowlist tests from
   `manifest_test.go`); assert behavior identical to today.

4. **`service_fallback_test.go`** — inject two fake sources controllable
   per method:
   - Both healthy ⇒ first source wins, `LastSourceUsed="github"`,
     `LastFallbacks` empty (for fresh state).
   - First manifest times out ⇒ second wins, `LastFallbacks` has one
     entry `(github, manifest, timeout)`.
   - First download first-byte times out ⇒ same, stage="download".
   - First download starts then goes slow ⇒ same.
   - First download succeeds but bytes don't match SHA256 ⇒
     second wins, fallback recorded with stage="verify" and
     `ErrSHA256Mismatch` wrapped reason.
   - Both fail at every stage ⇒ scheduler returns error;
     `LastFallbacks` has up to 5 entries; partial file removed.
   - Parent ctx cancelled mid-flight ⇒ scheduler returns
     `parent.Err()` without iterating remaining sources.
   - Fresh attempt after a prior fallback ⇒ prior `FallbackRecord`s
     retained in the rolling buffer.

5. **Manual sweep**: re-run the three source-lint tests
   (`service_source_test.go`, `installer_windows_source_test.go`, and
   any newly-discovered `*_source_test.go`) after the refactor —
   their substring assertions reference internal control flow that
   the new code paths preserve, but verify rather than assume.

Existing `internal/updater` tests that don't touch the moved
host-allowlist behavior must continue to pass unchanged.

## Migration / rollout

- Default config keeps `UPGRADE_GITHUB_ENABLED=false`, so day-1
  behavior in production is identical to today.
- This change publishes `latest.json` to the GitHub release for every
  tagged build, so the asset exists for canaries.
- Ops flips `UPGRADE_GITHUB_ENABLED=true` on canary launchers; the
  next polling tick uses GitHub first, CDN as fallback.
- After cross-region validation, default flips to `true` in a later
  release.

## Risks

- **Anonymous GitHub rate limit (60/h/IP)** in NATted environments
  where many launchers share an egress IP. Mitigated by polling
  cadence per launcher + automatic CDN fallback on any 403/429.
  Worst case: GitHub source is effectively unused in that
  environment; CDN still works.
- **GitHub asset CDN slow in some regions.** This is the entire point
  of the speed-window fallback; the threshold default (100 KB/s avg
  over 10s) is intentionally lenient so we don't ping-pong sources on
  every small slowdown.
- **Identical SHA256 across sources** is a publisher discipline issue.
  Verification will fail if they diverge, the source is recorded as a
  fallback, and the next source is tried — correct safe behavior,
  but worth documenting in the release runbook.
- **Removing host check from `Manifest.Validate()`** means any future
  caller of `Validate()` outside the source layer loses the
  AssetsHost guarantee. Today the only callers are the two sources
  and `DownloadAndStart`'s manifest sanity check; new call sites
  should consult a source for host validation.
