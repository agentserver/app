package updater

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// UpgradeConfig holds the env-only tunables for the upgrade sources.
// Default (zero-value except GitHubRepo + GitHubPolicy) leaves GitHub
// disabled — Service.effectiveSources falls back to the compat CDN
// shortcut.
type UpgradeConfig struct {
	GitHubEnabled bool
	GitHubRepo    string
	GitHubPolicy  SourcePolicy
}

// validRepoSlug matches "owner/repo" where each segment is 1..100 chars
// of GitHub-allowed characters. Defeats path-traversal shenanigans
// (`../etc/passwd` etc.) that would produce a suspicious API request URL.
var validRepoSlug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`)

// LoadUpgradeConfig reads env vars via the provided getter. Pass
// os.Getenv in production; tests pass a fake. Invalid values are
// silently ignored (fall back to default) so a malformed env var
// never disables the whole feature.
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
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitHubPolicy.ManifestTimeout = d
		}
	}
	if v := env("UPGRADE_GITHUB_FIRST_BYTE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitHubPolicy.FirstByteTimeout = d
		}
	}
	if v := env("UPGRADE_GITHUB_MIN_SPEED_BPS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			cfg.GitHubPolicy.MinSpeedBytesPerSec = n
		}
	}
	if v := env("UPGRADE_GITHUB_SPEED_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitHubPolicy.SpeedWindow = d
		}
	}
	return cfg
}

// BuildSources returns nil when GitHub is disabled — Service's compat
// shortcut then builds [cdnSource] lazily from ManifestURL + Client.
// When enabled, returns [githubSource, cdnSource]. Each source gets
// its OWN *http.Client with its OWN *http.Transport — sharing
// http.DefaultTransport would mean applyFirstByteTimeout's
// ResponseHeaderTimeout mutation on one source's clone bleeds into
// the other's connection pool (spec requirement: "Two sources do
// not share Transports").
func BuildSources(cfg UpgradeConfig) []Source {
	if !cfg.GitHubEnabled {
		return nil
	}
	return []Source{
		NewGitHubSource(cfg.GitHubRepo, "", newIsolatedHTTPClient(), cfg.GitHubPolicy),
		NewCDNSource(DefaultManifestURL, newIsolatedHTTPClient(), DefaultSourcePolicy()),
	}
}

// newIsolatedHTTPClient returns a client with a fresh Transport cloned
// from http.DefaultTransport — inherits sensible defaults (dialer,
// keep-alive, proxy) but is safe to mutate per-source.
func newIsolatedHTTPClient() *http.Client {
	transport := &http.Transport{}
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = dt.Clone()
	}
	return &http.Client{Transport: transport}
}
