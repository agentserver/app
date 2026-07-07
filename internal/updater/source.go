package updater

import (
	"context"
	"errors"
	"io"
	"net/http"
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

// noopProgress is a shared no-op sample handler used by tests and any
// call site that must pass a non-nil function but does not want
// samples. Sources that don't need the monitor should pass nil
// instead — see monitorRequired.
func noopProgress(SpeedSample) {}

// monitorRequired reports whether a source should spawn a speed monitor
// (and its countingReader body-wrap) for this download attempt.
// Returns true when:
//   - trip detection is armed (window > 0 AND minBPS > 0), OR
//   - a first-byte deadline is armed (FirstByteTimeout > 0) — the
//     monitor's countingReader is what fires onFirstByte to stop the
//     deadline timer; without it, a server that sends headers then
//     hangs the body forever escapes both mechanisms, OR
//   - the caller wants progress samples (onProgress != nil).
//
// Compat mode (all-zero policy + nil onProgress) skips the monitor
// entirely — no ticker goroutine allocation per download.
func monitorRequired(policy SourcePolicy, onProgress func(SpeedSample)) bool {
	if policy.MinSpeedBytesPerSec > 0 && policy.SpeedWindow > 0 {
		return true
	}
	if policy.FirstByteTimeout > 0 {
		return true
	}
	return onProgress != nil
}

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

// hasRealTransport reports whether client.Transport is a *http.Transport
// (or nil, which means net/http will use DefaultTransport, also real).
// False for custom RoundTrippers — the download loop then applies a
// request-context deadline for FirstByteTimeout as a fallback.
// Shared by cdnSource and githubSource; defined here to avoid drift.
func hasRealTransport(client *http.Client) bool {
	if client == nil || client.Transport == nil {
		return true
	}
	_, ok := client.Transport.(*http.Transport)
	return ok
}

// applyFirstByteTimeout returns a client whose Transport enforces
// ResponseHeaderTimeout when the base Transport is *http.Transport,
// or a client with the base RoundTripper preserved verbatim otherwise.
// This preserves test-only RoundTrippers such as assetsHostRewriteTransport
// (internal/updater/service_test.go) which would be lost by a naive clone.
// In the non-*Transport path, FirstByteTimeout is best-effort via the
// request context deadline instead.
//
// When firstByte <= 0 (compat-mode policy or explicit disable), return
// a shallow copy of base with no Transport mutation — avoids allocating
// a fresh *http.Transport (and therefore a fresh connection pool) per
// download attempt in compat mode.
func applyFirstByteTimeout(base *http.Client, firstByte time.Duration) *http.Client {
	c := *base
	if firstByte <= 0 {
		return &c
	}
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
