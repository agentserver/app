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

	"github.com/agentserver/agentserver-pkg/internal/appversion"
)

const defaultGitHubAPIBase = "https://api.github.com"

type githubSource struct {
	repo    string
	apiBase string
	client  *http.Client
	policy  SourcePolicy

	// installerHostMatch is the asset-host validator. Production value
	// is githubAssetHost; tests override to accept httptest.Server hosts.
	// When overridden by a test, memoized clients must be rebuilt —
	// see rebuildClients.
	installerHostMatch func(host string) bool

	// Memoized redirect-pinned + first-byte-timeout clients. Built once
	// at construction to avoid per-download Transport clone.
	api       *http.Client
	asset     *http.Client
	installer *http.Client
}

// NewGitHubSource returns a Source backed by a public GitHub release.
// apiBase defaults to https://api.github.com; tests override to point
// at an httptest.Server. The returned Source has its own *http.Transport
// (via applyFirstByteTimeout) so setting FirstByteTimeout does not
// affect other sources.
func NewGitHubSource(repo, apiBase string, client *http.Client, policy SourcePolicy) Source {
	if apiBase == "" {
		apiBase = defaultGitHubAPIBase
	}
	if client == nil {
		client = http.DefaultClient
	}
	s := &githubSource{
		repo:               repo,
		apiBase:            apiBase,
		client:             client,
		policy:             policy,
		installerHostMatch: githubAssetHost,
	}
	s.rebuildClients()
	return s
}

// rebuildClients (re)builds the three memoized clients. Called from
// the constructor and, in tests only, after installerHostMatch is
// swapped for a permissive matcher.
func (s *githubSource) rebuildClients() {
	s.api = s.redirectPinned(s.client, githubAPIHost)
	s.asset = s.redirectPinned(s.client, s.installerHostMatch)
	s.installer = applyFirstByteTimeout(s.asset, s.policy.FirstByteTimeout)
}

func (s *githubSource) Name() string { return "github" }

// isRealTransport delegates to package-level hasRealTransport.
func (s *githubSource) isRealTransport() bool { return hasRealTransport(s.client) }

// normalizeHost canonicalizes a URL host for whitelist comparison.
// Lowercase + trim trailing dot (DNS-dot bypass defense).
func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// githubAssetHost is the SINGLE source of truth for "is this URL a
// legitimate GitHub-hosted asset?" Matches:
//   - github.com (production browser_download_url host)
//   - codeload.github.com (occasional archive redirect)
//   - any subdomain of githubusercontent.com (release-assets.,
//     objects., raw., and future renames)
// Does NOT match api.github.com or bare "githubusercontent.com".
func githubAssetHost(host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	if h == "github.com" || h == "codeload.github.com" {
		return true
	}
	const suffix = ".githubusercontent.com"
	if !strings.HasSuffix(h, suffix) {
		return false
	}
	sub := strings.TrimSuffix(h, suffix)
	if sub == "" || strings.ContainsAny(sub, "/@") {
		return false
	}
	return true
}

// githubAPIHost is used ONLY for the initial /repos/.../releases/latest
// request. Also accepts any *.githubusercontent.com for redirects from
// api.github.com hitting the asset CDN.
func githubAPIHost(host string) bool {
	if normalizeHost(host) == "api.github.com" {
		return true
	}
	return githubAssetHost(host)
}

func (s *githubSource) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentserver-app/"+appversion.Version)
}

func (s *githubSource) apiClient() *http.Client       { return s.api }
func (s *githubSource) assetClient() *http.Client     { return s.asset }
func (s *githubSource) installerClient() *http.Client { return s.installer }

func (s *githubSource) redirectPinned(base *http.Client, hostOK func(string) bool) *http.Client {
	client := *base
	prior := base.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// P1: enforce https on every hop. A 302 to http:// downgrades
		// the next leg to cleartext. The installer's SHA256 protects
		// the final bytes only IF the manifest itself was fetched
		// over TLS — a poisoned latest.json served over http could
		// point at any allowlisted GitHub binary the attacker knows
		// the SHA of.
		if req.URL.Scheme != "https" {
			return fmt.Errorf("%w: redirect scheme %q, only https allowed", ErrHostNotAllowed, req.URL.Scheme)
		}
		if req.URL.User != nil {
			return fmt.Errorf("%w: userinfo not permitted", ErrHostNotAllowed)
		}
		if !hostOK(req.URL.Hostname()) {
			return fmt.Errorf("%w: %s", ErrHostNotAllowed, req.URL.Hostname())
		}
		// Preserve Accept + User-Agent across redirects. Go's stdlib
		// strips Authorization on cross-host; Accept + UA aren't in
		// the sensitive-header list, but we re-install defensively
		// per hop so GitHub asset CDN never sees a bare request.
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
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubReleaseResponse struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

func (s *githubSource) FetchManifest(ctx context.Context) (Manifest, error) {
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
	// Defense-in-depth: enforce https on the asset URL BEFORE the
	// first request. Manifest.Validate() only runs on m (the parsed
	// latest.json body); assetURL comes from the API response and
	// has no other trust boundary before we hit it. The redirect
	// callback also blocks http:// on subsequent hops, but a first
	// hop to plaintext would already have been sent by then.
	if assetU.Scheme != "https" {
		return Manifest{}, fmt.Errorf("%w: asset scheme %q, only https allowed", ErrHostNotAllowed, assetU.Scheme)
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
	// Installer host validated at DownloadInstaller time (via
	// installerHostMatch) so tests can inject a permissive matcher.
	return m, nil
}

// fetchRelease is one HTTP hop with its own ManifestTimeout budget.
func (s *githubSource) fetchRelease(parent context.Context) (githubReleaseResponse, error) {
	ctx := parent
	if s.policy.ManifestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, s.policy.ManifestTimeout)
		defer cancel()
	}

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
	ctx := parent
	if s.policy.ManifestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, s.policy.ManifestTimeout)
		defer cancel()
	}

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
	needMonitor := monitorRequired(s.policy, onProgress)
	if onProgress == nil {
		onProgress = noopProgress
	}

	dlCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// First-body-byte deadline armed on all paths (production +
	// fallback). See cdnSource.firstByteDeadlineCtx for semantics.
	reqCtx, markFirstByte, stopFB, fbTripped := s.firstByteDeadlineCtx(dlCtx)
	defer stopFB()

	var monitor *speedMonitor
	var monitorDone chan struct{}
	if needMonitor {
		monitor = newSpeedMonitor(s.policy, cancel, onProgress)
		monitor.onFirstByte = markFirstByte
		monitorDone = make(chan struct{})
		go func() { monitor.run(dlCtx); close(monitorDone) }()
		defer func() { cancel(); <-monitorDone }()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	s.setHeaders(req)
	resp, err := s.installerClient().Do(req)
	if err != nil {
		return s.classifyDownload(ctx, monitor, fbTripped, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github download: unexpected status %s", resp.Status)
	}
	var body io.Reader = io.LimitReader(resp.Body, m.Size+1)
	if monitor != nil {
		body = monitor.wrap(body)
	}
	n, err := io.Copy(dst, body)
	if err != nil {
		return s.classifyDownload(ctx, monitor, fbTripped, err)
	}
	if n > m.Size {
		return fmt.Errorf("github download: response larger than declared size")
	}
	return nil
}

// classifyFetch takes both the parent ctx (caller-supplied) and the
// per-request ctx (has the ManifestTimeout deadline). If the per-request
// deadline fired, return ErrFetchTimeout; else honor parent cancellation;
// else return err verbatim.
func (s *githubSource) classifyFetch(parent context.Context, req context.Context, err error) error {
	if errors.Is(req.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrFetchTimeout, err)
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	return err
}

// firstByteDeadlineCtx: see cdnSource.firstByteDeadlineCtx for full
// semantics. Armed on all paths (production + fallback) because
// ResponseHeaderTimeout only covers headers.
func (s *githubSource) firstByteDeadlineCtx(parent context.Context) (ctx context.Context, markFirstByte func(), stop func(), tripped func() bool) {
	if s.policy.FirstByteTimeout <= 0 {
		return parent, func() {}, func() {}, func() bool { return false }
	}
	ctx, cancel := context.WithCancel(parent)
	var decided atomic.Bool
	var timerFired atomic.Bool
	timer := time.AfterFunc(s.policy.FirstByteTimeout, func() {
		if decided.CompareAndSwap(false, true) {
			timerFired.Store(true)
			cancel()
		}
	})
	markFirstByte = func() {
		if decided.CompareAndSwap(false, true) {
			timer.Stop()
		}
	}
	stop = func() {
		timer.Stop()
		cancel()
	}
	tripped = timerFired.Load
	return
}

func (s *githubSource) classifyDownload(parent context.Context, monitor *speedMonitor, fbTripped func() bool, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if fbTripped != nil && fbTripped() {
		return fmt.Errorf("%w: first-byte deadline elapsed", ErrFetchTimeout)
	}
	if monitor != nil && monitor.Tripped() {
		return fmt.Errorf("%w: %v", ErrSlowDownload, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrFetchTimeout, err)
	}
	return err
}
