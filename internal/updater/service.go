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
	// Caller-manifest version guard — enforced ONLY in compat mode.
	// In multi-source mode each source re-fetches its own manifest and
	// runs its own per-source version check; aborting here on the
	// caller's (possibly stale) manifest would defeat the "fallback
	// survives caller drift" property. Compat mode has no re-fetch,
	// so the guard remains the only safety net there.
	if len(s.Sources) == 0 {
		cmp, err := CompareVersions(m.Version, s.CurrentVersion)
		if err != nil {
			return s.saveError(now, fmt.Errorf("invalid current version: %w", err))
		}
		if cmp <= 0 {
			return s.saveError(now, fmt.Errorf("update version %s is not newer than current version %s", m.Version, s.CurrentVersion))
		}
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

	// fallbacks accumulates across per-source attempts; tryOneSource
	// reads it (via closure) to build terminal-state save calls.
	var fallbacks []FallbackRecord
	var lastErr error

	// attemptOutcome describes what tryOneSource decided. Exactly one
	// of terminalState (source succeeded / a hard error to bubble up
	// as a terminal state), fallback (record and try next), or done
	// (nothing more to try, error came from ctx) is set per call.
	type attemptOutcome struct {
		terminalState *State // set ⇒ return this state
		terminalErr   error  // if terminalState set, paired error (may be nil on success)
		fallbackSet   bool   // true ⇒ record fallback + continue loop
		fallbackStage string
		fallbackErr   error
		continueLoop  bool // false ⇒ break out of loop with lastErr
	}

	// tryOneSource runs a single source attempt end-to-end. Its
	// own function scope means the per-attempt `defer os.Remove(tempPath)`
	// fires at attempt exit, not accumulating across loop iterations —
	// safe to add a 3rd source later without unbounded defer stack.
	tryOneSource := func(src Source) attemptOutcome {
		var freshM Manifest
		if compatMode {
			freshM = m
		} else {
			var err error
			freshM, err = src.FetchManifest(ctx)
			if err != nil {
				if ctx.Err() != nil {
					st, ie := s.saveErrorWithFallbacks(now, ctx.Err(), prior.LastFallbacks, fallbacks)
					return attemptOutcome{terminalState: &st, terminalErr: ie}
				}
				return attemptOutcome{fallbackSet: true, fallbackStage: "manifest", fallbackErr: err, continueLoop: true}
			}
		}
		vcmp, err := CompareVersions(freshM.Version, s.CurrentVersion)
		if err != nil {
			return attemptOutcome{fallbackSet: true, fallbackStage: "version", fallbackErr: err, continueLoop: true}
		}
		if vcmp <= 0 {
			return attemptOutcome{fallbackSet: true, fallbackStage: "version",
				fallbackErr: fmt.Errorf("source manifest version %s not newer than current %s", freshM.Version, s.CurrentVersion),
				continueLoop: true}
		}

		finalPath, err := installerCachePath(s.CacheDir, freshM)
		if err != nil {
			st, ie := s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
			return attemptOutcome{terminalState: &st, terminalErr: ie}
		}
		temp, err := os.CreateTemp(s.CacheDir, filepath.Base(finalPath)+".*.tmp")
		if err != nil {
			st, ie := s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
			return attemptOutcome{terminalState: &st, terminalErr: ie}
		}
		tempPath := temp.Name()
		// Per-attempt cleanup: fires when tryOneSource returns, not on
		// enclosing DownloadAndStart exit. Bounded to ONE outstanding
		// temp file at a time regardless of how many sources exist.
		promoted := false
		defer func() {
			if !promoted {
				_ = os.Remove(tempPath)
			}
		}()

		attemptCtx, cancel := context.WithCancel(ctx)
		// nil onProgress ⇒ sources may skip the monitor goroutine when
		// policy also disables trip detection (compat mode).
		err = src.DownloadInstaller(attemptCtx, freshM, temp, nil)
		cancel()
		if closeErr := temp.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			if ctx.Err() != nil {
				st, ie := s.saveErrorWithFallbacks(now, ctx.Err(), prior.LastFallbacks, fallbacks)
				return attemptOutcome{terminalState: &st, terminalErr: ie}
			}
			return attemptOutcome{fallbackSet: true, fallbackStage: "download", fallbackErr: err, continueLoop: true}
		}
		if err := verifyInstaller(tempPath, freshM); err != nil {
			return attemptOutcome{fallbackSet: true, fallbackStage: "verify",
				fallbackErr: fmt.Errorf("%w: %v", ErrSHA256Mismatch, err), continueLoop: true}
		}
		if err := replaceFile(tempPath, finalPath); err != nil {
			st, ie := s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
			return attemptOutcome{terminalState: &st, terminalErr: ie}
		}
		if s.BeforeInstallerStart != nil {
			if err := s.BeforeInstallerStart(ctx, freshM, finalPath); err != nil {
				st, ie := s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
				return attemptOutcome{terminalState: &st, terminalErr: ie}
			}
		}
		start := s.StartInstaller
		startContext := ctx
		if start == nil {
			start = StartInstaller
			startContext = context.Background()
		}
		if err := start(startContext, finalPath); err != nil {
			st, ie := s.saveErrorWithFallbacks(now, err, prior.LastFallbacks, fallbacks)
			return attemptOutcome{terminalState: &st, terminalErr: ie}
		}
		promoted = true
		state := State{
			CurrentVersion: s.CurrentVersion,
			Status:         StatusInstallerStarted,
			Update:         availableFromManifest(freshM),
			LastSourceUsed: src.Name(),
			LastFallbacks:  mergeFallbacks(prior.LastFallbacks, fallbacks),
		}
		st, ie := s.saveFinalState(now, state)
		return attemptOutcome{terminalState: &st, terminalErr: ie}
	}

	for _, src := range sources {
		out := tryOneSource(src)
		if out.terminalState != nil {
			return *out.terminalState, out.terminalErr
		}
		if out.fallbackSet {
			fallbacks = s.appendFallback(fallbacks, src.Name(), out.fallbackStage, out.fallbackErr)
			lastErr = out.fallbackErr
		}
		if !out.continueLoop {
			break
		}
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

// saveError writes a StatusError state.
//
// Variadic freshFallbacks: pass fresh FallbackRecords from an
// in-progress source loop; they will be merged onto the prior
// rolling history (capped at maxFallbackHistory). Pre-loop callers
// omit the argument entirely — prior history is preserved unchanged.
//
// This single method replaces the earlier saveError /
// saveErrorWithFallbacks pair. The split was fragile: any future
// terminal-error branch inside a source loop that forgot the
// "-WithFallbacks" variant silently dropped the accumulated
// fallbacks and lost the ops visibility the feature is designed
// for. Now the always-merge behavior means callers can never
// pick the wrong one.
func (s Service) saveError(now time.Time, err error, freshFallbacks ...[]FallbackRecord) (State, error) {
	state := State{
		CurrentVersion: s.CurrentVersion,
		LastCheckedAt:  now,
		Status:         StatusError,
		LastError:      err.Error(),
	}
	var fresh []FallbackRecord
	for _, f := range freshFallbacks {
		fresh = append(fresh, f...)
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
		state.LastSourceUsed = prior.LastSourceUsed
		state.LastFallbacks = mergeFallbacks(prior.LastFallbacks, fresh)
	} else {
		state.LastFallbacks = fresh
	}
	if saveErr := s.saveState(state); saveErr != nil {
		return state, errors.Join(err, fmt.Errorf("save error state: %w", saveErr))
	}
	return state, err
}

// saveErrorWithFallbacks is retained as a thin wrapper for existing
// call sites; new code should use the variadic saveError directly.
func (s Service) saveErrorWithFallbacks(now time.Time, err error, prior []FallbackRecord, fresh []FallbackRecord) (State, error) {
	// prior is intentionally ignored — saveError re-loads state under
	// serviceStateMu (held for the whole flow) and merges from there.
	_ = prior
	return s.saveError(now, err, fresh)
}
