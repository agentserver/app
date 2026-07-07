package updater

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
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

// insecureTLSClient trusts any TLS cert — required for tests that use
// httptest.NewTLSServer (self-signed).
func insecureTLSClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

const testRepo = "agentserver/app"

// githubMock serves both the /repos/.../releases/latest endpoint and
// the browser_download_url assets in one httptest.Server.
type githubMock struct {
	t                    *testing.T
	server               *httptest.Server
	installerBody        []byte
	manifestBody         []byte // rendered latest.json; setManifest fills it
	manifestStatus       int
	slowManifestAsset    bool          // when true, /assets/latest.json sleeps slowManifestAssetDelay
	slowManifestAssetDur time.Duration // default 500ms when slowManifestAsset is set
	slowReleaseAPI       bool          // when true, /repos/.../releases/latest sleeps slowReleaseAPIDur
	slowReleaseAPIDur    time.Duration
	requireHeaders       bool // when true, /repos/.../releases/latest 403s missing Accept+UA
}

func newGitHubMock(t *testing.T) *githubMock {
	m := &githubMock{t: t, manifestStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/releases/latest", testRepo), func(w http.ResponseWriter, r *http.Request) {
		if m.slowReleaseAPI {
			d := m.slowReleaseAPIDur
			if d <= 0 {
				d = 500 * time.Millisecond
			}
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}
		if m.requireHeaders {
			if r.Header.Get("Accept") != "application/vnd.github+json" ||
				!strings.HasPrefix(r.Header.Get("User-Agent"), "agentserver-app/") {
				http.Error(w, "missing headers", http.StatusForbidden)
				return
			}
		}
		if m.manifestStatus != http.StatusOK {
			if m.manifestStatus == http.StatusForbidden {
				w.Header().Set("X-RateLimit-Remaining", "0")
			}
			http.Error(w, "err", m.manifestStatus)
			return
		}
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
		if m.slowManifestAsset {
			d := m.slowManifestAssetDur
			if d <= 0 {
				d = 500 * time.Millisecond
			}
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(m.manifestBody)
	})
	mux.HandleFunc("/assets/setup.exe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(m.installerBody)))
		w.Write(m.installerBody)
	})
	m.server = httptest.NewTLSServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

// setManifest computes SHA256 of body and stores a latest.json pointing
// at installerURL.
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

// newTestGitHubSource returns a source pointing at the mock with the
// production asset host matcher — tests that use httptest.Server for
// the installer must override installerHostMatch to accept 127.0.0.1.
func newTestGitHubSource(mock *githubMock) *githubSource {
	return NewGitHubSource(testRepo, mock.server.URL, insecureTLSClient(), DefaultSourcePolicy()).(*githubSource)
}

// permissiveHost accepts every host — used in tests where the mock's
// httptest.Server URL is not a real GitHub host.
func permissiveHost(string) bool { return true }

func TestGitHubSourceNameIsGithub(t *testing.T) {
	mock := newGitHubMock(t)
	if src := newTestGitHubSource(mock); src.Name() != "github" {
		t.Fatalf("Name()=%q, want github", src.Name())
	}
}

func TestGitHubSourceHappyPath(t *testing.T) {
	mock := newGitHubMock(t)
	body := []byte(strings.Repeat("x", 4096))
	mock.setManifest(mock.server.URL+"/assets/setup.exe", body)
	src := newTestGitHubSource(mock)
	src.installerHostMatch = permissiveHost
	src.rebuildClients()

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
	src.installerHostMatch = permissiveHost
	src.rebuildClients()

	if _, err := src.FetchManifest(context.Background()); err != nil {
		t.Fatalf("FetchManifest expected to succeed with headers: %v", err)
	}
}

func TestGitHubSourceRateLimit403(t *testing.T) {
	mock := newGitHubMock(t)
	mock.manifestStatus = http.StatusForbidden
	src := newTestGitHubSource(mock)

	_, err := src.FetchManifest(context.Background())
	if err == nil || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v; want ErrRateLimited", err)
	}
}

func TestGitHubSourceRateLimit429(t *testing.T) {
	mock := newGitHubMock(t)
	mock.manifestStatus = http.StatusTooManyRequests
	src := newTestGitHubSource(mock)

	_, err := src.FetchManifest(context.Background())
	if err == nil || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v; want ErrRateLimited on 429", err)
	}
}

func TestGitHubSourceManifestTimeoutFires(t *testing.T) {
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	src := NewGitHubSource(testRepo, slow.URL, insecureTLSClient(), policy).(*githubSource)

	start := time.Now()
	_, err := src.FetchManifest(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrFetchTimeout) {
		t.Fatalf("err=%v; want ErrFetchTimeout wrap", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("FetchManifest ran %v, expected ~50ms", elapsed)
	}
}

func TestGitHubSourceManifestTimeoutIsPerRequest(t *testing.T) {
	// Distinguish per-hop from shared budget:
	//   - Budget: 200ms
	//   - Hop 1 (release API): sleeps 150ms
	//   - Hop 2 (latest.json):  sleeps 100ms
	//
	// Per-hop budget: both hops fit under 200ms individually
	//   → total elapsed ≈ 250ms, request SUCCEEDS.
	// Shared budget: hop 1 consumes 150ms of shared 200ms
	//   → hop 2 has only 50ms left, times out at ~200ms elapsed
	//   → request FAILS with ErrFetchTimeout.
	mock := newGitHubMock(t)
	mock.slowReleaseAPI = true
	mock.slowReleaseAPIDur = 150 * time.Millisecond
	mock.slowManifestAsset = true
	mock.slowManifestAssetDur = 100 * time.Millisecond
	body := []byte("payload")
	mock.setManifest(mock.server.URL+"/assets/setup.exe", body)
	policy := DefaultSourcePolicy()
	policy.ManifestTimeout = 200 * time.Millisecond
	src := NewGitHubSource(testRepo, mock.server.URL, insecureTLSClient(), policy).(*githubSource)
	src.installerHostMatch = permissiveHost
	src.rebuildClients()

	start := time.Now()
	m, err := src.FetchManifest(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("FetchManifest err=%v after %v — timeout is shared across hops, not per-hop", err, elapsed)
	}
	if m.Version == "" {
		t.Fatalf("empty manifest returned")
	}
	// Sanity: elapsed should be ~250ms (150 + 100). Both hops ran.
	if elapsed < 200*time.Millisecond {
		t.Fatalf("elapsed %v — hops did not both run sequentially", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed %v — took too long, something else broke", elapsed)
	}
}

func TestGitHubSourceRejectsUnwhitelistedInstallerHost(t *testing.T) {
	mock := newGitHubMock(t)
	body := []byte("payload")
	mock.setManifest("https://evil.example.com/setup.exe", body)
	src := newTestGitHubSource(mock)
	// Do NOT override installerHostMatch — production matcher must reject.

	m, err := src.FetchManifest(context.Background())
	// FetchManifest also does host check on installer URL (userinfo);
	// with a foreign host it should still succeed at fetch (only
	// userinfo rejected inline). Actual host reject fires in DownloadInstaller.
	if err != nil {
		// If FetchManifest reject happens earlier (asset URL fails on
		// installerHostMatch), that's also correct.
		if !errors.Is(err, ErrHostNotAllowed) {
			t.Fatalf("err=%v; want ErrHostNotAllowed or nil", err)
		}
		return
	}
	err = src.DownloadInstaller(context.Background(), m, io.Discard, nil)
	if err == nil || !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed", err)
	}
}

func TestGithubAssetHostMatcher(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"github.com", true},
		{"codeload.github.com", true},
		{"objects.githubusercontent.com", true},
		{"release-assets.githubusercontent.com", true},
		{"GitHub.com", true},
		{"github.com.", true},
		{"api.github.com", false},
		{"evil.githubusercontent.com.attacker.com", false},
		{"githubusercontent.com", false},
		{"", false},
		{"[::1]", false},
		{"192.168.1.1", false},
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
	mock.setManifest("https://good@github.com/setup.exe", body)
	src := newTestGitHubSource(mock)
	src.installerHostMatch = permissiveHost // even if host allowed, userinfo rejected
	src.rebuildClients()

	_, err := src.FetchManifest(context.Background())
	if err == nil || !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed for userinfo URL", err)
	}
}

func TestGitHubSourceRejectsInstallerLargerThanSize(t *testing.T) {
	mock := newGitHubMock(t)
	realBody := []byte(strings.Repeat("x", 100))
	sum := sha256.Sum256(realBody[:10])
	man := Manifest{
		Version: "1.0.0",
		URL:     mock.server.URL + "/assets/setup.exe",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    10, // lie about size
	}
	b, _ := json.Marshal(man)
	mock.manifestBody = b
	mock.installerBody = realBody
	src := newTestGitHubSource(mock)
	src.installerHostMatch = permissiveHost
	src.rebuildClients()

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
	var secondHopSaw http.Header
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHopSaw = r.Header.Clone()
		w.Write([]byte("body"))
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	src := NewGitHubSource(testRepo, "", insecureTLSClient(), DefaultSourcePolicy()).(*githubSource)
	src.installerHostMatch = permissiveHost
	src.rebuildClients()

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
