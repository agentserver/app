package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const DefaultManifestURL = "https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json"

// The manifest is small today, but this leaves room for future optional fields
// and release notes while bounding hostile metadata responses.
const manifestMaxBytes = 64 * 1024

// maxFallbackHistory caps LastFallbacks; older entries are evicted.
const maxFallbackHistory = 5

type Service struct {
	CurrentVersion       string
	ManifestURL          string
	CacheDir             string
	State                stateStore
	Client               *http.Client
	StartInstaller       func(context.Context, string) error
	BeforeInstallerStart func(context.Context, Manifest, string) error
	Now                  func() time.Time
	AutoCheckEvery       time.Duration

	// Sources is the ordered list of upgrade origins the scheduler
	// tries. When nil, the scheduler synthesizes a single CDN source
	// via the compat shortcut (see effectiveSources) so existing
	// fixtures using ManifestURL + Client keep working unchanged.
	Sources []Source
}

var serviceStateMu sync.Mutex

type stateStore interface {
	Load() (State, error)
	Save(State) error
}

// compatCDNPolicy is the SourcePolicy applied when the compat shortcut
// synthesizes a cdnSource. All zeros ⇒ no ManifestTimeout wrap, no
// FirstByteTimeout, no speed monitor — byte-identical to today's
// behavior. Preserves the "existing tests unchanged" guarantee.
func compatCDNPolicy() SourcePolicy {
	return SourcePolicy{}
}

// effectiveSources returns s.Sources if set, else a single-element
// slice with a lazily-built CDN source using ManifestURL + Client.
// Called on every Check / DownloadAndStart invocation; the constructor
// call is cheap (no I/O, no allocations of significance).
func (s Service) effectiveSources() []Source {
	if len(s.Sources) > 0 {
		return s.Sources
	}
	manifestURL := s.ManifestURL
	if manifestURL == "" {
		manifestURL = DefaultManifestURL
	}
	return []Source{NewCDNSource(manifestURL, s.client(), compatCDNPolicy())}
}

// appendFallback appends a FallbackRecord and trims the buffer to
// maxFallbackHistory. Never nil-safe; caller owns the slice.
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

// mergeFallbacks concatenates prior + fresh and caps at maxFallbackHistory.
// Used to preserve rolling history across attempts.
func mergeFallbacks(prior, fresh []FallbackRecord) []FallbackRecord {
	combined := append([]FallbackRecord{}, prior...)
	combined = append(combined, fresh...)
	if len(combined) > maxFallbackHistory {
		combined = combined[len(combined)-maxFallbackHistory:]
	}
	return combined
}

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
				return s.saveErrorWithFallbacks(now, ctx.Err(), prior.LastFallbacks, fallbacks)
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
	return s.saveErrorWithFallbacks(now, lastErr, prior.LastFallbacks, fallbacks)
}

func NormalizeStateForCurrentVersion(state State, currentVersion string) State {
	state.CurrentVersion = currentVersion
	switch state.Status {
	case StatusChecking, StatusDownloading:
		state.Status = StatusIdle
		state.Update = nil
		state.LastError = ""
		return state
	case StatusInstallerStarted:
		if state.Update == nil {
			return state
		}
		cmp, err := CompareVersions(state.Update.Version, currentVersion)
		if err == nil && cmp <= 0 {
			state.Status = StatusLatest
			state.Update = nil
			state.LastError = ""
		}
		return state
	case StatusAvailable:
	default:
		return state
	}
	if state.Update == nil {
		state.Status = StatusLatest
		state.Update = nil
		return state
	}
	cmp, err := CompareVersions(state.Update.Version, currentVersion)
	if err != nil || cmp <= 0 {
		state.Status = StatusLatest
		state.Update = nil
	}
	return state
}

func (s Service) DownloadAndStart(ctx context.Context, m Manifest) (State, error) {
	serviceStateMu.Lock()
	defer serviceStateMu.Unlock()

	now := s.now()
	if err := m.Validate(); err != nil {
		return s.saveError(now, err)
	}
	// Caller-manifest version guard — PRESERVED from today's behavior.
	// A UI bug that replays a stale manifest must not trigger download.
	cmp, err := CompareVersions(m.Version, s.CurrentVersion)
	if err != nil {
		return s.saveError(now, fmt.Errorf("invalid current version: %w", err))
	}
	if cmp <= 0 {
		return s.saveError(now, fmt.Errorf("update version %s is not newer than current version %s", m.Version, s.CurrentVersion))
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

	// Per-source manifest resolution:
	// - Compat mode (Sources==nil): trust the caller's `m` verbatim,
	//   no re-fetch. Preserves today's byte-identical behavior for
	//   existing tests that stub only the installer download path.
	// - Multi-source mode: each source re-fetches its OWN authoritative
	//   manifest. Ensures a CDN download uses a CDN URL/hash even if
	//   the caller's `m` came from GitHub (or vice versa).
	sources := s.effectiveSources()
	compatMode := len(s.Sources) == 0

	var fallbacks []FallbackRecord
	var lastErr error
	for _, src := range sources {
		var freshM Manifest
		if compatMode {
			freshM = m
		} else {
			var err error
			freshM, err = src.FetchManifest(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return s.saveErrorWithFallbacks(now, ctx.Err(), prior.LastFallbacks, fallbacks)
				}
				fallbacks = s.appendFallback(fallbacks, src.Name(), "manifest", err)
				lastErr = err
				continue
			}
		}
		vcmp, err := CompareVersions(freshM.Version, s.CurrentVersion)
		if err != nil {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "version", err)
			lastErr = err
			continue
		}
		if vcmp <= 0 {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "version",
				fmt.Errorf("source manifest version %s not newer than current %s", freshM.Version, s.CurrentVersion))
			lastErr = fmt.Errorf("source %s has no newer version", src.Name())
			continue
		}

		finalPath, err := installerCachePath(s.CacheDir, freshM)
		if err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		temp, err := os.CreateTemp(s.CacheDir, filepath.Base(finalPath)+".*.tmp")
		if err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		tempPath := temp.Name()
		// promoted/defer PRESERVED from today's behavior — covers every
		// unhappy exit from replaceFile / BeforeInstallerStart / start
		// / terminal-return path. Multiple sources means at most N
		// leftover temps on disk between failing continue and function
		// return (bounded by source count, currently 2).
		promoted := false
		defer func() {
			if !promoted {
				_ = os.Remove(tempPath)
			}
		}()

		attemptCtx, cancel := context.WithCancel(ctx)
		err = src.DownloadInstaller(attemptCtx, freshM, temp, noopProgress)
		cancel()
		if closeErr := temp.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			if ctx.Err() != nil {
				return s.saveErrorWithFallbacks(now, ctx.Err(), prior.LastFallbacks, fallbacks)
			}
			fallbacks = s.appendFallback(fallbacks, src.Name(), "download", err)
			lastErr = err
			continue
		}
		if err := verifyInstaller(tempPath, freshM); err != nil {
			fallbacks = s.appendFallback(fallbacks, src.Name(), "verify",
				fmt.Errorf("%w: %v", ErrSHA256Mismatch, err))
			lastErr = err
			continue
		}
		if err := replaceFile(tempPath, finalPath); err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		if s.BeforeInstallerStart != nil {
			if err := s.BeforeInstallerStart(ctx, freshM, finalPath); err != nil {
				return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
			}
		}
		start := s.StartInstaller
		startContext := ctx
		if start == nil {
			start = StartInstaller
			startContext = context.Background()
		}
		if err := start(startContext, finalPath); err != nil {
			return s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
		}
		promoted = true
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
	return s.saveErrorWithFallbacks(now, lastErr, prior.LastFallbacks, fallbacks)
}

func (s Service) autoCheckEvery() time.Duration {
	if s.AutoCheckEvery > 0 {
		return s.AutoCheckEvery
	}
	return 24 * time.Hour
}

func verifyInstaller(path string, m Manifest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != m.Size {
		return fmt.Errorf("installer size mismatch: got %d, want %d", n, m.Size)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, m.SHA256) {
		return fmt.Errorf("installer sha256 mismatch: got %s, want %s", got, strings.ToLower(m.SHA256))
	}
	return nil
}

func installerCachePath(cacheDir string, m Manifest) (string, error) {
	u, err := url.Parse(m.URL)
	if err != nil {
		return "", err
	}
	name := filepath.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		name = "agentserver-app-" + m.Version + "-setup.exe"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".exe") {
		name += ".exe"
	}
	name = filepath.Base(name)
	return filepath.Join(cacheDir, name), nil
}

func availableFromManifest(m Manifest) *AvailableUpdate {
	return &AvailableUpdate{
		Version: m.Version,
		URL:     m.URL,
		SHA256:  m.SHA256,
		Size:    m.Size,
		Notes:   m.Notes,
	}
}

func (s Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Service) loadState() (State, error) {
	if s.State == nil {
		return State{Status: StatusIdle}, nil
	}
	return s.State.Load()
}

func (s Service) saveState(state State) error {
	if s.State == nil {
		return nil
	}
	return s.State.Save(state)
}

func (s Service) saveFinalState(now time.Time, state State) (State, error) {
	if err := s.saveState(state); err != nil {
		return s.saveError(now, err)
	}
	return state, nil
}

// saveError writes a StatusError state with no fallback history. Used
// only by pre-loop guards (validation, state IO, ctx already cancelled
// before any source was tried). Terminal errors INSIDE the source loop
// must use saveErrorWithFallbacks so ops can see attempt history.
func (s Service) saveError(now time.Time, err error) (State, error) {
	state := State{
		CurrentVersion: s.CurrentVersion,
		LastCheckedAt:  now,
		Status:         StatusError,
		LastError:      err.Error(),
	}
	if prior, loadErr := s.loadState(); loadErr == nil {
		if !prior.LastCheckedAt.IsZero() {
			state.LastCheckedAt = prior.LastCheckedAt
		}
		if prior.Update != nil {
			if cmp, cmpErr := CompareVersions(prior.Update.Version, s.CurrentVersion); cmpErr == nil && cmp > 0 {
				state.Update = prior.Update
			}
		}
		// Preserve prior rolling history — pre-loop errors shouldn't
		// wipe out ops visibility from earlier attempts.
		state.LastSourceUsed = prior.LastSourceUsed
		state.LastFallbacks = prior.LastFallbacks
	}
	if saveErr := s.saveState(state); saveErr != nil {
		return state, errors.Join(err, fmt.Errorf("save error state: %w", saveErr))
	}
	return state, err
}

// saveErrorWithFallbacks writes a StatusError state that PRESERVES the
// rolling fallback history — merges prior + fresh capped at
// maxFallbackHistory. Every terminal error INSIDE the source loop
// (both Check and DownloadAndStart) must route through this so ops
// can see days later why every attempt failed.
func (s Service) saveErrorWithFallbacks(now time.Time, err error, prior []FallbackRecord, fresh []FallbackRecord) (State, error) {
	state := State{
		CurrentVersion: s.CurrentVersion,
		LastCheckedAt:  now,
		Status:         StatusError,
		LastError:      err.Error(),
		LastFallbacks:  mergeFallbacks(prior, fresh),
	}
	if p, loadErr := s.loadState(); loadErr == nil {
		if !p.LastCheckedAt.IsZero() {
			state.LastCheckedAt = p.LastCheckedAt
		}
		if p.Update != nil {
			if cmp, cmpErr := CompareVersions(p.Update.Version, s.CurrentVersion); cmpErr == nil && cmp > 0 {
				state.Update = p.Update
			}
		}
		state.LastSourceUsed = p.LastSourceUsed
	}
	if saveErr := s.saveState(state); saveErr != nil {
		return state, errors.Join(err, fmt.Errorf("save error state: %w", saveErr))
	}
	return state, err
}
