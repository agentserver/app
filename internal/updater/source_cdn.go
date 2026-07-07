package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type cdnSource struct {
	manifestURL string
	client      *http.Client
	policy      SourcePolicy

	// pinned + install are memoized at construction time to avoid
	// per-download clone of *http.Transport (and its connection pool)
	// on every DownloadInstaller call.
	pinned  *http.Client
	install *http.Client
}

// NewCDNSource returns a Source backed by the internal CDN
// (assets.agent.cs.ac.cn). Callers pass the manifest URL (typically
// DefaultManifestURL), an *http.Client (nil ⇒ http.DefaultClient), and
// a SourcePolicy. The returned Source constructs its own redirect
// validator; a nil-policy (all zeros) disables timeouts and the speed
// monitor — used by the Service compat shortcut to preserve today's
// download behavior.
func NewCDNSource(manifestURL string, client *http.Client, policy SourcePolicy) Source {
	if client == nil {
		client = http.DefaultClient
	}
	s := &cdnSource{manifestURL: manifestURL, client: client, policy: policy}
	s.pinned = s.buildPinnedClient()
	s.install = applyFirstByteTimeout(s.pinned, s.policy.FirstByteTimeout)
	return s
}

func (s *cdnSource) Name() string { return "cdn" }

// isRealTransport delegates to package-level hasRealTransport.
func (s *cdnSource) isRealTransport() bool { return hasRealTransport(s.client) }

// validateInstallerURL enforces the CDN's host allowlist. Rejects
// userinfo, non-https, and any host other than AssetsHost (case-fold,
// trailing-dot tolerant).
func (s *cdnSource) validateInstallerURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("cdn: invalid installer url")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("cdn: installer url must use https")
	}
	if u.User != nil {
		return fmt.Errorf("%w: userinfo not allowed", ErrHostNotAllowed)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host != AssetsHost {
		return fmt.Errorf("%w: installer host %q not allowed", ErrHostNotAllowed, u.Hostname())
	}
	return nil
}

func (s *cdnSource) FetchManifest(ctx context.Context) (Manifest, error) {
	if s.policy.ManifestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.policy.ManifestTimeout)
		defer cancel()
	}

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
	needMonitor := monitorRequired(s.policy, onProgress)
	if onProgress == nil {
		onProgress = noopProgress
	}
	dlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var monitor *speedMonitor
	var monitorDone chan struct{}
	if needMonitor {
		monitor = newSpeedMonitor(s.policy, cancel, onProgress)
		monitorDone = make(chan struct{})
		go func() { monitor.run(dlCtx); close(monitorDone) }()
		defer func() { cancel(); <-monitorDone }()
	}

	// When the underlying Transport is not *http.Transport (test path),
	// applyFirstByteTimeout returned an unmodified client. Arm a
	// first-byte-only cancellation: a timer that cancels reqCtx iff
	// the "headers received" flag hasn't been set. Once client.Do
	// returns (headers received), mark the flag and stop the timer —
	// body reads then proceed under the parent dlCtx alone. This
	// mirrors production's ResponseHeaderTimeout scope.
	reqCtx, markHeadersReceived, stopFB := s.firstByteDeadlineCtx(dlCtx)
	defer stopFB()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	resp, err := s.installerClient().Do(req)
	if err != nil {
		return s.classify(ctx, monitor, err)
	}
	markHeadersReceived()
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cdn download: unexpected status %s", resp.Status)
	}
	var body io.Reader = io.LimitReader(resp.Body, m.Size+1)
	if monitor != nil {
		body = monitor.wrap(body)
	}
	n, err := io.Copy(dst, body)
	if err != nil {
		return s.classify(ctx, monitor, err)
	}
	if n > m.Size {
		return fmt.Errorf("cdn download: response larger than declared size")
	}
	return nil
}

// firstByteDeadlineCtx arms a first-byte-only cancellation on the
// returned ctx. Semantics:
//   - If FirstByteTimeout elapses AND markHeadersReceived was not
//     yet called, the ctx is cancelled — future request I/O returns
//     context.Canceled.
//   - If markHeadersReceived is called first, the timer is stopped
//     and the ctx is NEVER cancelled by this helper — body reads
//     proceed under the parent (dlCtx) alone.
//   - stop is always called via defer and is a no-op after
//     markHeadersReceived.
//
// Used only in the non-*Transport fallback (test path). Production
// gets first-byte enforcement via http.Transport.ResponseHeaderTimeout
// inside applyFirstByteTimeout, so this helper is a no-op there.
//
// Do NOT use context.WithTimeout's returned cancel — cancelling a
// child ctx cancels the whole request, including in-flight body
// reads (resp.Body is bound to req.Context()).
func (s *cdnSource) firstByteDeadlineCtx(parent context.Context) (ctx context.Context, markHeadersReceived, stop func()) {
	if s.isRealTransport() || s.policy.FirstByteTimeout <= 0 {
		return parent, func() {}, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	var gotHeaders atomic.Bool
	timer := time.AfterFunc(s.policy.FirstByteTimeout, func() {
		if !gotHeaders.Load() {
			cancel()
		}
	})
	markHeadersReceived = func() {
		gotHeaders.Store(true)
		timer.Stop()
	}
	stop = func() {
		timer.Stop()
		cancel()
	}
	return ctx, markHeadersReceived, stop
}

// classify implements the cancellation-precedence rule: parent ctx first,
// then Tripped(). Safe with a nil monitor (compat mode never launches
// one). A first-byte deadline that fired (fallback path) is wrapped
// as ErrFetchTimeout so state reason distinguishes it from a raw dial
// error.
func (s *cdnSource) classify(parent context.Context, monitor *speedMonitor, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if monitor != nil && monitor.Tripped() {
		return fmt.Errorf("%w: %v", ErrSlowDownload, err)
	}
	// If the wrapped err is context.DeadlineExceeded (from our
	// firstByteDeadlineCtx), classify as fetch timeout for ops
	// visibility.
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrFetchTimeout, err)
	}
	return err
}

// pinnedClient returns the memoized redirect-pinned client.
func (s *cdnSource) pinnedClient() *http.Client { return s.pinned }

// installerClient returns the memoized installer client (pinned +
// FirstByteTimeout applied).
func (s *cdnSource) installerClient() *http.Client { return s.install }

// buildPinnedClient constructs the CheckRedirect-wrapped client once.
// Called from the constructor; do not call per-request.
func (s *cdnSource) buildPinnedClient() *http.Client {
	return s.redirectPinned(s.client)
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
