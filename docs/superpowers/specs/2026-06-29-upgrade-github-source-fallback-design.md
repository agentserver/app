# Upgrade: GitHub Release Source with CDN Fallback

**Date:** 2026-06-29
**Status:** Draft v3 (post review-round-2)
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
   disabled (default). Existing `internal/updater` tests must continue
   to pass unchanged.
4. Surface which source was used and why fallback happened, in
   `update-state.json` and the console HTTP API.
5. Make the source set extensible: adding a third source (e.g. an R2
   mirror) should mean implementing one interface, not editing scheduler
   logic.
6. Publish `latest.json` to the GitHub release as part of this change so
   the GitHub source has something to fetch on day 1. This is the
   first automated release-publish step in this repository; today's
   CDN publish is out-of-tree.

## Non-Goals

- Collapsing the two-step Check / Install UI flow into a single call.
  Both `Check` and `DownloadAndStart` are extended to iterate sources
  independently; the public API surface of `Service` is preserved.
- Resuming a CDN download from where a GitHub download stalled. The
  cancelled GitHub partial is discarded; CDN downloads from byte 0.
- Auth tokens for GitHub. The repo is public; anonymous queries only.
  Anonymous 60/h IP rate limit is treated as failure → fallback.
- A user-facing "try GitHub first" toggle in the UI. Source order is a
  process-level env config, not a per-update choice.
- Introducing a dependency on `internal/download/resumable.go`. The
  updater does not import that package today and won't after this
  change; speed monitoring is implemented locally.
- Plumbing a download-progress callback up to the console / frontend.
  `SpeedSample` exists for source-internal monitor logic and tests
  today; piping it to a SSE / progress UI is a follow-up.
- Prerelease / channel selection. `/releases/latest` returns the most
  recent non-prerelease, non-draft release; this design is
  stable-only by intent.
- User-configurable CDN URL. `AssetsHost` and `DefaultManifestURL`
  remain hardcoded; tests override them by constructing a CDN source
  directly, not via env.

## Architecture

```
internal/updater/
├── service.go             (modified)  Sources field; Check & DownloadAndStart iterate
├── manifest.go            (modified)  Remove host check from Validate (move to source)
├── state.go               (modified)  Add LastSourceUsed, LastFallbacks
├── config.go              (new)       UpgradeConfig + LoadUpgradeConfig + BuildSources
├── source.go              (new)       Source interface, SpeedSample, sentinels
├── source_cdn.go          (new)       Today's CDN behavior, behind Source
├── source_github.go       (new)       GitHub release: fetch latest.json asset
├── speed_monitor.go       (new)       Sliding-window speed measurement + clock/tick injection
├── source_cdn_test.go     (new)       Inherits host-allowlist tests migrated from manifest_test.go
├── source_github_test.go  (new)
├── speed_monitor_test.go  (new)
└── service_fallback_test.go (new)     Fake-source scheduler tests

cmd/launcher/main.go       (modified)  newCompletedUpdater (lines 341–348) reads env, sets Sources

packaging/windows/         (modified)  Author latest.json alongside .exe at package time
scripts/windows-package-common.sh (modified)  Assemble latest.json inputs
.github/workflows/         (new file)  Release workflow uploads {latest.json, .exe} to the GitHub release
```

### Disposition of existing `service.go` symbols

| Symbol | v3 disposition |
|---|---|
| `DefaultManifestURL` const | Kept; consumed by CDN source builder as default. |
| `Service.ManifestURL`, `Service.Client` fields | **Kept** as compatibility shortcuts. If `Sources==nil` at first use, `Service` lazily constructs `[cdnSource]` from `ManifestURL` + `Client` — so every existing fixture in `service_test.go` (28 sites using `assetsHostClient`) and `cmd/launcher/main_test.go` (6 sites setting `ManifestURL`) keeps working with zero diff. |
| `Service.fetchManifest` | **Deleted** from service.go; equivalent logic moves to `source_cdn.go`. |
| `Service.manifestDownloadClient` | Deleted; CDN source uses its own client. |
| `Service.downloadInstaller` | Deleted; equivalent logic moves to `source_cdn.go`. |
| `Service.installerDownloadClient` | Deleted; CDN source uses its own client. |
| `Service.redirectPinnedAssetsClient` | Moved into `source_cdn.go` as a private helper (CDN source's redirect validator). |
| `verifyInstaller` | **Kept in service.go**, called by scheduler after `Source.DownloadInstaller` returns nil. SHA256 is scheduler responsibility. |
| `installerCachePath`, `availableFromManifest` | Kept. |
| `serviceStateMu` | Kept, unchanged scope (see "Concurrency" below). |

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
// Implementations MUST be safe to share across concurrent calls to
// Service.Check / Service.DownloadAndStart, because console/update.go
// shallow-copies Service to compose per-request callbacks. Concretely:
// hold no per-call mutable state on the Source itself; per-attempt
// state (speed monitor, temp files) lives on the stack of the method
// call. The Source's *http.Client / *http.Transport are safe to share
// because net/http is goroutine-safe.
type Source interface {
    Name() string
    FetchManifest(ctx context.Context) (Manifest, error)

    // DownloadInstaller writes installer bytes to dst using the
    // manifest THIS source returned from FetchManifest. Callers MUST
    // NOT pass a manifest from a different source. SHA256 verification
    // is NOT the source's responsibility — the scheduler verifies
    // after this returns nil.
    //
    // Cancellation precedence: on any io error, the source first
    // checks whether the PARENT context is cancelled and returns
    // parent.Err() in that case. Only if the parent is still live
    // and the internal speed monitor's Tripped() flag is true does
    // the source wrap ErrSlowDownload. This prevents user-initiated
    // shutdown from being misclassified as a slow-download fallback.
    DownloadInstaller(
        ctx context.Context,
        m Manifest,
        dst io.Writer,
        onProgress func(SpeedSample),
    ) error
}

// noopProgress is the scheduler's default onProgress callback. Defined
// in source.go as `func noopProgress(SpeedSample) {}`. Tests pass
// their own callback to observe the wire.

// Sentinel errors. Sources wrap these; scheduler records the wrapped
// kind in FallbackRecord.Reason so /update-state can show why fallback
// happened. Callers that want to programmatically detect a specific
// failure can use errors.Is on the returned error from Check /
// DownloadAndStart; the scheduler preserves the sentinel chain in
// the error it aggregates and returns.
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
return values, and observable state transitions. Inside, each loops
over `s.effectiveSources()` (which returns `s.Sources` if set, else
the lazily-built `[cdnSource]` from the compat shortcut).

**Per-source manifest binding (GAP-10 fix):** each source fetches its
own manifest and, on the download path, `DownloadInstaller` receives
the manifest **that same source produced**. There is no cross-source
manifest reuse. A CDN download never sees a GitHub URL and vice
versa.

**Version comparison (GAP-9 decision):** the first source that returns
a non-error manifest is authoritative. If GitHub replies
`version == current`, that's a successful "already latest" answer
and the scheduler does NOT continue to CDN looking for a newer
answer. Fallback is triggered by source *failure*, not by
disagreement. Publisher discipline (both sources publish the same
version + sha256) is enforced by the release pipeline change below.

```go
// Check, after the AutoCheckEvery skip-threshold gate (unchanged):
var fallbacks []FallbackRecord
for _, src := range s.effectiveSources() {
    ctx, cancel := context.WithCancel(parent)
    m, err := src.FetchManifest(ctx)
    cancel()
    if err != nil {
        if parent.Err() != nil { return s.saveError(now, parent.Err()) }
        fallbacks = append(fallbacks, s.record(src.Name(), "manifest", err))
        continue
    }
    // Compare version — same logic as today's Check (StatusLatest vs
    // StatusAvailable). Either outcome is a "successful check" that
    // stops iteration.
    return s.saveCheckOutcome(now, m, src.Name(), fallbacks)
}
return s.saveError(now, fmt.Errorf("all sources failed: %v", fallbacks))

// DownloadAndStart: each source runs its own fetch+download+verify.
// The manifest passed by the caller (from a prior Check) is the
// STARTING POINT — but if the first source's fetch produces a
// different manifest than what the UI showed the user, we still
// download that source's manifest (it's the authoritative one for
// that source's bytes). The version-newer-than-current guard runs
// per-source manifest.
for _, src := range s.effectiveSources() {
    ctx, cancel := context.WithCancel(parent)
    freshM, err := src.FetchManifest(ctx)
    if err != nil {
        cancel()
        if parent.Err() != nil { return s.saveError(now, parent.Err()) }
        fallbacks = append(fallbacks, s.record(src.Name(), "manifest", err))
        continue
    }
    if err := requireNewerThanCurrent(freshM, s.CurrentVersion); err != nil {
        cancel()
        fallbacks = append(fallbacks, s.record(src.Name(), "version", err))
        continue
    }
    tempPath := newTempPath(freshM)
    if err := src.DownloadInstaller(ctx, freshM, temp, noopProgress); err != nil {
        cancel(); os.Remove(tempPath)
        if parent.Err() != nil { return s.saveError(now, parent.Err()) }
        fallbacks = append(fallbacks, s.record(src.Name(), "download", err))
        continue
    }
    cancel()
    if err := verifyInstaller(tempPath, freshM); err != nil {
        os.Remove(tempPath)
        fallbacks = append(fallbacks, s.record(src.Name(), "verify",
            fmt.Errorf("%w: %v", ErrSHA256Mismatch, err)))
        continue
    }
    // … existing replaceFile / BeforeInstallerStart / StartInstaller path,
    // called with freshM (not the caller's stale m) …
    return s.saveInstallerStarted(now, freshM, src.Name(), fallbacks)
}
return s.saveError(now, fmt.Errorf("all sources failed: %v", fallbacks))
```

Key constraints:

- One `ctx, cancel` per source attempt.
- Partial files at `tempPath` are removed before the next source
  tries and after the final failure.
- SHA256 verification stays in the scheduler; mismatch is recorded
  with stage="verify" and the next source is tried.
- **State-file save granularity:** the scheduler accumulates
  `fallbacks` in memory across per-source attempts and writes the
  state file **once at the end** (in `saveCheckOutcome` /
  `saveInstallerStarted` / `saveError`). A crash mid-iteration means
  the state file reflects the previous successful attempt; the next
  polling tick re-attempts and records fresh fallbacks. This
  preserves today's atomic-replace semantics of `state.go` and keeps
  the state file consistent even under concurrent readers.
- **StatusDownloading UI behavior:** `StatusDownloading` is written
  once at the start of `DownloadAndStart` and stays that value across
  all per-source attempts. No per-source `StatusFallback` is
  introduced — the UI sees a single download in progress and the
  fallback history appears in `last_fallbacks` after completion.

## Concurrency

`serviceStateMu` (package-level mutex in `service.go`) is held for
the entire duration of `Check` and `DownloadAndStart`, unchanged.
Multi-source iteration happens under the same lock. This is an
intentional choice: "one update flow at a time per process" is
today's semantics and preserving it avoids interleaved state-file
writes. A pathological case (GitHub fetch takes 5s + CDN fetch takes
5s + CDN download takes 30s under a slow-fallback = ~40s under
lock, blocking a concurrent `CheckUpdate` request from the console)
is acceptable — `CheckUpdate` requests are rare (24h cadence + user
click), and the lock scope matches today.

## Source policy (timeouts and speed thresholds)

```go
type SourcePolicy struct {
    ManifestTimeout     time.Duration // default 5s
    FirstByteTimeout    time.Duration // default 10s
    MinSpeedBytesPerSec int64         // default 100 * 1024 (100 KB/s)
    SpeedWindow         time.Duration // default 10s
}
```

- Each `Source` impl holds its own `SourcePolicy`, its own
  `*http.Client`, **and its own `*http.Transport`**. Two sources do
  not share Transports — sharing would mean setting
  `ResponseHeaderTimeout` on one Source's Transport bleeds into the
  other Source's behavior.
- `ManifestTimeout` is a per-request deadline on the manifest call,
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
    now      func() time.Time          // injected; production = time.Now
    tick     <-chan time.Time          // injected; production = time.Tick(1s)
    samples  []sample                  // ring buffer, 1/tick
    bytes    atomic.Int64
    cancel   context.CancelFunc
    onSample func(SpeedSample)
    tripped  atomic.Bool               // source reads on ctx.Err()
}

func (m *speedMonitor) wrap(r io.Reader) io.Reader   // updates m.bytes per Read
func (m *speedMonitor) run(ctx context.Context)       // ticks until ctx.Done
func (m *speedMonitor) Tripped() bool                 // post-run inspection
```

The injected `tick` and `now` let tests drive the monitor with a
hand-controlled `chan time.Time`, eliminating real-time flakiness.

## GitHub source details

- **Manifest endpoint:**
  `https://api.github.com/repos/<repo>/releases/latest`
  where `<repo>` defaults to `agentserver/app` and can be overridden
  via `UPGRADE_GITHUB_REPO`. Parse the JSON, find the asset with
  `name == "latest.json"`, GET its `browser_download_url`
  (≤ `manifestMaxBytes`, reusing the existing service.go constant),
  unmarshal into the existing `Manifest` struct.

- **Required request headers** (both API and asset GETs):
  - `Accept: application/vnd.github+json`
  - `User-Agent: agentserver-app/<appversion.Version>` — GitHub
    rejects anonymous requests without a UA.

- **`/releases/latest` semantics:** returns most recent non-prerelease,
  non-draft. Prerelease channels are explicitly not supported (see
  Non-Goals).

- **Manifest URL field semantics:** the `url` field in the GitHub
  copy of `latest.json` MUST point at a GitHub-hosted location
  (host matches the installer whitelist below). The CDN copy of
  `latest.json` continues to point at `assets.agent.cs.ac.cn`. Both
  copies share `version`, `sha256`, `size`, `notes`. The release
  pipeline produces both files from one source of truth.

- **Host whitelists (GitHub source only):**
  - Manifest fetch hosts: `api.github.com` and any subdomain of
    `githubusercontent.com` (handles the 302 from the asset
    `browser_download_url`).
  - Installer URL host (inside `latest.json`): any subdomain of
    `githubusercontent.com` (`release-assets.githubusercontent.com`
    is today's hop; the wildcard tolerates GitHub renaming) plus
    `github.com`.
  - Implemented as a small "host matcher" function inside
    `source_github.go`.

- **HTTP 403 / 429:** both wrap `ErrRateLimited`. The reason string
  in `FallbackRecord.Reason` records finer detail
  (`"github 403: x-ratelimit-remaining=0"` etc.) for ops visibility.

- **Redirects:** `http.Client.CheckRedirect` validates each hop
  against the installer whitelist. Unexpected hosts wrap
  `ErrHostNotAllowed`.

- **No internal retries.** One miss ⇒ fall through to CDN; the
  launcher's existing scheduler retries the whole flow on its own
  cadence.

## CDN source details

- Verbatim port of today's `fetchManifest` + download loop into
  `source_cdn.go`. Same `*http.Client` pinning, same
  `manifestMaxBytes` cap, same `io.LimitReader(resp.Body, m.Size+1)`
  size guard.
- Whitelist is `AssetsHost` (existing constant from `manifest.go`),
  consulted inside the CDN source's own redirect validator.
- **CDN source construction accepts URL + Client via constructor
  args, not env.** Test-only overrides come through the constructor
  (`updater.NewCDNSource(url, client, policy)`); production uses
  `DefaultManifestURL`. There is no `UPGRADE_CDN_*` env override —
  changing the CDN URL requires changing the host whitelist too,
  which is a code change, not a config change.
- When `s.effectiveSources() == [cdnSource]` (GitHub disabled, the
  default), observable behavior is identical to today: same manifest
  URL, same SHA256 verification, same `update-state.json` fields
  except the new optional `last_source_used: "cdn"` and (empty)
  `last_fallbacks`.

## Manifest.Validate() — chosen split (option a)

`Manifest.Validate()` is reduced to format-only checks: version parse,
SHA256 format, size positive, URL has https scheme + non-empty host.
**Host allowlist is removed from `Validate()`** and moved into each
source's redirect / response check.

### Tests migrated to source_cdn_test.go

From `manifest_test.go`, the following AssetsHost-specific tests move
to `source_cdn_test.go` and are re-expressed against the CDN source's
validator (assertions equivalent; unit-under-test changes):

- `TestManifestValidateAcceptsAssetsHTTPSInstaller` (line 9)
- `TestManifestValidateAcceptsMixedCaseAssetsHost` (line 22)
- `TestManifestValidateRejectsAssetsHostSuffixBypass` (line 144)
- `TestManifestValidateRejectsAssetsHostUserinfoBypass` (line 155)
- `TestManifestValidateRejectsURLOutsideAssetsHost` (line 178)

Format-only tests stay in `manifest_test.go`:
`TestManifestValidateRejectsInvalidVersion`,
`TestManifestValidateRejectsPaddedVersion`,
`TestManifestValidateRejectsZeroPaddedVersion`,
`TestManifestValidateRejectsMissingSHA256`,
`TestManifestValidateRejectsPaddedSHA256`,
`TestManifestValidateRejectsWrongLengthSHA256`,
`TestManifestValidateRejectsNonHexSHA256`,
`TestManifestValidateRejectsNonHTTPSURL`,
`TestManifestValidateRejectsPaddedURL`,
`TestManifestValidateRejectsNegativeSize`,
`TestManifestProductionCodeHasNoInstallerHostOverride`.

### Tests that must continue to pass unchanged

The compat shortcut (`ManifestURL` + `Client` → lazily-built
`[cdnSource]`) means these fixture sites do **not** need editing:

- `internal/updater/service_test.go` — 28 fixtures using
  `assetsHostClient(t, server)` and 20 setting
  `Service{ManifestURL: server.URL, Client: ..., …}`.
- `cmd/launcher/main_test.go` — 6 fixtures setting `ManifestURL`.
- `internal/updater/service_source_test.go` and
  `installer_windows_source_test.go` — substring lints on `service.go`
  referencing `start = StartInstaller` and
  `startContext = context.Background()`. Both control-flow markers
  are preserved in the refactor. Manual sweep confirms this after
  the port.

## State and observability

`internal/updater/state.go` `State` adds:

```go
type FallbackRecord struct {
    Source string    `json:"source"`           // "github" / "cdn"
    Stage  string    `json:"stage"`            // "manifest" / "version" / "download" / "verify"
    Reason string    `json:"reason"`           // wrapped sentinel + free-form detail
    Tried  time.Time `json:"tried"`            // Service.Now(), not time.Now()
}

type State struct {
    // existing fields …
    LastSourceUsed string           `json:"last_source_used,omitempty"`
    LastFallbacks  []FallbackRecord `json:"last_fallbacks,omitempty"`
}
```

- **`LastFallbacks` semantics:** a rolling history across *all* recent
  attempts (not just the current attempt). Capped to the 5 most
  recent entries. A successful attempt that previously fell back
  preserves prior failures in the buffer, so ops can see "the last
  3 GitHub attempts went to CDN because of rate limiting" days
  later. Timestamps ride with each record; buffer rotation drops
  the oldest entry, not its timestamp.
- `LastSourceUsed` is set from the source that ultimately succeeded
  in the most recent `Check` or `DownloadAndStart`. Empty until a
  source succeeds.
- `internal/console/update.go` exposes both fields verbatim through
  the existing `/update-state` payload — JSON tags carry them; no
  controller change is required.

## Error handling and cancellation

- `parent` context comes from the caller (24h scheduler in
  `cmd/launcher`, or a console request). Scheduler derives a
  per-source `(ctx, cancel)`.
- Source returns:
  - `nil` ⇒ source succeeded; scheduler proceeds (download → verify
    → replace / start).
  - non-`nil` ⇒ scheduler first checks `parent.Err()`; if the parent
    is cancelled it returns immediately without further iteration.
    Otherwise records `FallbackRecord` and tries the next source.
- Cancellation precedence inside a source: on any I/O error, the
  source reads `parent.Err()`. If non-nil, return `parent.Err()`
  directly (no `ErrSlowDownload` wrap). Only if parent is live AND
  `monitor.Tripped()` is true does the source return an error
  wrapping `ErrSlowDownload`.
- Partial files at `tempPath` are removed before each next-source
  attempt and after the final failure.

## Configuration (env-first, YAML-free for now)

The existing `internal/slave/config.go` is write-only (serialises to
YAML for loom). There is no config-loader in the process today.
Rather than introduce one, upgrade configuration is
**environment-variable only**, and only for the GitHub source (CDN
is not user-configurable):

```
UPGRADE_GITHUB_ENABLED=true|false        # default false
UPGRADE_GITHUB_REPO=owner/repo           # default agentserver/app
UPGRADE_GITHUB_MANIFEST_TIMEOUT=5s       # optional
UPGRADE_GITHUB_FIRST_BYTE_TIMEOUT=10s    # optional
UPGRADE_GITHUB_MIN_SPEED_BPS=102400      # optional
UPGRADE_GITHUB_SPEED_WINDOW=10s          # optional
```

Reading happens in a new `internal/updater/config.go`:

```go
type UpgradeConfig struct {
    GitHubEnabled bool
    GitHubRepo    string
    GitHubPolicy  SourcePolicy
    // CDN URL/host is not env-configurable; see Non-Goals.
}

// env is os.Getenv in production; tests pass a fake.
func LoadUpgradeConfig(env func(string) string) UpgradeConfig { ... }

// BuildSources returns nil when GitHub is disabled — Service's compat
// shortcut then builds [cdnSource] lazily from ManifestURL + Client.
// Returns [githubSource, cdnSource] when enabled; CDN source is built
// from DefaultManifestURL and http.DefaultClient in that case.
func BuildSources(cfg UpgradeConfig) []Source { ... }
```

`cmd/launcher/main.go::newCompletedUpdater` (lines 341–348) becomes:

```go
cfg := updater.LoadUpgradeConfig(os.Getenv)
return &updater.Service{
    CurrentVersion: appversion.Version,
    ManifestURL:    updater.DefaultManifestURL, // kept for compat shortcut
    CacheDir:       p.UpdatesCacheDir,
    State:          updater.NewStateStore(p.UpdateStateFile),
    Sources:        updater.BuildSources(cfg), // nil when GitHub disabled
}
```

## Release-pipeline change (required, first automated publish step)

The repo currently has no automated release publish (grepped
`scripts/`, `packaging/`, `.github/` for `latest.json` — zero hits).
Adding the GitHub source therefore requires adding the first
automated publish step. Two files gain changes and one file is new:

- `scripts/windows-package-common.sh` — after the installer is built,
  compute its SHA256, template `latest.json` from a new
  `packaging/windows/latest.json.tmpl` (fields: version, url, sha256,
  size, notes). Two invocations: one templates the CDN URL
  (existing manual CDN publish path), one templates the GitHub
  asset URL. Both files land in `dist/`.
- `packaging/windows/latest.json.tmpl` — new; the template.
- `.github/workflows/release.yml` — new; on tag push, runs the
  packaging script and uploads `{latest.json, agentserver-app-*.exe}`
  to the GitHub release via `gh release upload`.

Rollout is gated on this pipeline: `UPGRADE_GITHUB_ENABLED=true` is
only meaningful after the first release that includes a published
`latest.json`. Until then, the flag is safe to flip (GitHub source
gets 404 on the asset → immediate CDN fallback → correct behavior,
just wasted round trip).

## Testing strategy (TDD order)

Write tests first, in this order:

1. **`speed_monitor_test.go`** — drive with injected fake clock and
   tick channel:
   - No samples before window full ⇒ no cancel.
   - Window full, trailing avg below threshold ⇒ `cancel()` called
     and `Tripped()` reports true.
   - Window full, trailing avg above threshold ⇒ no cancel,
     `Tripped()` false.
   - `onSample` fires once per tick.

2. **`source_github_test.go`** — `httptest.Server` simulating the
   GitHub API and asset redirect chain:
   - Happy path: valid `latest.json`, asset 302 →
     `release-assets.githubusercontent.com` mock, body downloads.
   - Manifest 403 (with and without `x-ratelimit-remaining: 0`) ⇒
     `ErrRateLimited`.
   - Manifest 429 ⇒ `ErrRateLimited`.
   - Missing `Accept` / `User-Agent` on request path ⇒ mock returns
     403 to prove headers are set.
   - Manifest response > `manifestMaxBytes` ⇒ error.
   - Redirect to non-whitelisted host ⇒ `ErrHostNotAllowed`.
   - Manifest exceeds `ManifestTimeout` ⇒ `ErrFetchTimeout`.
   - Download throttled below `MinSpeedBytesPerSec` for a full
     window ⇒ `ErrSlowDownload`.
   - Parent ctx cancelled while download in flight ⇒ returns
     parent.Err(), NOT `ErrSlowDownload`, even if monitor also
     tripped in the same tick.

3. **`source_cdn_test.go`** — the migrated AssetsHost tests plus a
   CDN happy-path.

4. **`service_fallback_test.go`** — inject two fake sources:
   - Both healthy ⇒ first wins, `LastSourceUsed="github"`,
     `LastFallbacks` empty (fresh state).
   - First manifest times out ⇒ second wins, one fallback entry
     `(github, manifest, timeout)`.
   - First manifest returns older version ⇒ per-source manifest
     binding means second source still refetches; version-not-newer
     recorded as fallback stage="version".
   - First download first-byte times out ⇒ second wins.
   - First download starts then goes slow ⇒ second wins.
   - First download succeeds but sha256 mismatches ⇒ second wins,
     stage="verify", `ErrSHA256Mismatch` wrapped.
   - Both fail everywhere ⇒ error; up to 5 fallback entries;
     partial file removed.
   - Parent ctx cancelled mid-flight ⇒ returns `parent.Err()`,
     no further iteration, no `ErrSlowDownload` misclassification.
   - Fresh attempt after prior fallbacks ⇒ prior records retained
     up to buffer cap, oldest evicted.
   - GitHub returns `version == current` ⇒ StatusLatest, CDN not
     tried (authoritative first-non-error).
   - `Sources == nil` (compat shortcut) ⇒ Service builds
     `[cdnSource]` from `ManifestURL`+`Client` and behaves like
     today.

5. **Manual sweep** after the port: run
   `go test ./internal/updater/... ./cmd/launcher/...` and confirm
   `service_source_test.go` and `installer_windows_source_test.go`
   substring lints still pass without edits.

## Migration / rollout

- Default `UPGRADE_GITHUB_ENABLED=false`; day-1 behavior in
  production is identical to today.
- This change publishes `latest.json` to the GitHub release starting
  with the first tag after merge.
- Ops flips `UPGRADE_GITHUB_ENABLED=true` on canary launchers; next
  polling tick uses GitHub first, CDN as fallback.
- After cross-region validation, default flips to `true` in a later
  release.

## Risks

- **Anonymous GitHub rate limit (60/h/IP)** in NATted environments
  where many launchers share an egress IP. Mitigated by per-launcher
  polling cadence + automatic CDN fallback on any 403/429. Worst
  case: GitHub source is effectively unused in that environment;
  CDN still works.
- **GitHub asset CDN slow in some regions.** Point of the
  speed-window fallback; defaults (100 KB/s avg over 10s) are
  intentionally lenient to avoid ping-ponging.
- **Identical SHA256 across sources** is a publisher discipline
  issue. Verification failure records a fallback with stage="verify"
  and the next source is tried — safe, but flag it in the release
  runbook.
- **Removing host check from `Manifest.Validate()`** means any
  future caller of `Validate()` outside the source layer loses the
  AssetsHost guarantee. Today the only callers are the two sources
  and `DownloadAndStart`'s manifest sanity check; new call sites
  must consult a source for host validation.
- **GitHub returning stale manifest** (release-yml published a new
  tag but assets uploaded out of order): version comparison
  per-source means a stale "no update" answer from GitHub would
  block CDN. This is a publish-order bug, not a runtime bug; the
  release workflow uploads `latest.json` **last**, after the .exe
  is present and its SHA256 verified.
