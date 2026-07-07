package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
// validator; a nil-policy (all zeros) disables timeouts and the speed
// monitor — used by the Service compat shortcut to preserve today's
// download behavior.
func NewCDNSource(manifestURL string, client *http.Client, policy SourcePolicy) Source {
	if client == nil {
		client = http.DefaultClient
	}
	return &cdnSource{manifestURL: manifestURL, client: client, policy: policy}
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
	// applyFirstByteTimeout returned an unmodified client. Fall back
	// to a context deadline for the first byte via WithTimeout.
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
	resp, err := s.installerClient().Do(req)
	if err != nil {
		return s.classify(ctx, monitor, err)
	}
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

// classify implements the cancellation-precedence rule: parent ctx first,
// then Tripped(). Safe with a nil monitor (compat mode never launches
// one).
func (s *cdnSource) classify(parent context.Context, monitor *speedMonitor, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if monitor != nil && monitor.Tripped() {
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
// when the base Transport is *http.Transport; else returns the pinned
// client unchanged and the caller uses a request context deadline.
func (s *cdnSource) installerClient() *http.Client {
	return applyFirstByteTimeout(s.pinnedClient(), s.policy.FirstByteTimeout)
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
