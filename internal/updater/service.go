package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
}

var serviceStateMu sync.Mutex

type stateStore interface {
	Load() (State, error)
	Save(State) error
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
	}
	if err := s.saveState(checking); err != nil {
		return s.saveError(now, err)
	}

	manifest, err := s.fetchManifest(ctx)
	if err != nil {
		return s.saveError(now, err)
	}
	cmp, err := CompareVersions(manifest.Version, s.CurrentVersion)
	if err != nil {
		return s.saveError(now, err)
	}
	if cmp <= 0 {
		state := State{
			CurrentVersion: s.CurrentVersion,
			LastCheckedAt:  now,
			Status:         StatusLatest,
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
	}
	return s.saveFinalState(now, state)
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

	downloading := State{
		CurrentVersion: s.CurrentVersion,
		Status:         StatusDownloading,
		Update:         availableFromManifest(m),
	}
	if err := s.saveState(downloading); err != nil {
		return s.saveError(now, err)
	}

	finalPath, err := installerCachePath(s.CacheDir, m)
	if err != nil {
		return s.saveError(now, err)
	}
	temp, err := os.CreateTemp(s.CacheDir, filepath.Base(finalPath)+".*.tmp")
	if err != nil {
		return s.saveError(now, err)
	}
	tempPath := temp.Name()
	promoted := false
	defer func() {
		if !promoted {
			_ = os.Remove(tempPath)
		}
	}()
	if err := s.downloadInstaller(ctx, m, temp); err != nil {
		_ = temp.Close()
		return s.saveError(now, err)
	}
	if err := temp.Close(); err != nil {
		return s.saveError(now, err)
	}
	if err := verifyInstaller(tempPath, m); err != nil {
		return s.saveError(now, err)
	}
	if err := replaceFile(tempPath, finalPath); err != nil {
		return s.saveError(now, err)
	}
	promoted = true

	if s.BeforeInstallerStart != nil {
		if err := s.BeforeInstallerStart(ctx, m, finalPath); err != nil {
			return s.saveError(now, err)
		}
	}

	start := s.StartInstaller
	startContext := ctx
	if start == nil {
		start = StartInstaller
		startContext = context.Background()
	}
	if err := start(startContext, finalPath); err != nil {
		return s.saveError(now, err)
	}
	state := State{
		CurrentVersion: s.CurrentVersion,
		Status:         StatusInstallerStarted,
		Update:         availableFromManifest(m),
	}
	return s.saveFinalState(now, state)
}

func (s Service) fetchManifest(ctx context.Context) (Manifest, error) {
	manifestURL := s.ManifestURL
	if manifestURL == "" {
		manifestURL = DefaultManifestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return Manifest{}, err
	}
	resp, err := s.manifestDownloadClient().Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("fetch manifest: unexpected status %s", resp.Status)
	}
	var manifest Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, manifestMaxBytes)).Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (s Service) manifestDownloadClient() *http.Client {
	return s.redirectPinnedAssetsClient()
}

func (s Service) downloadInstaller(ctx context.Context, m Manifest, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	client := s.installerDownloadClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download installer: unexpected status %s", resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, m.Size+1))
	if err != nil {
		return err
	}
	if n > m.Size {
		return fmt.Errorf("installer response larger than declared size: got more than %d bytes", m.Size)
	}
	return nil
}

func (s Service) installerDownloadClient() *http.Client {
	return s.redirectPinnedAssetsClient()
}

func (s Service) redirectPinnedAssetsClient() *http.Client {
	base := s.client()
	client := *base
	priorCheckRedirect := base.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateInstallerURL(req.URL.String()); err != nil {
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
	}
	if saveErr := s.saveState(state); saveErr != nil {
		return state, errors.Join(err, fmt.Errorf("save error state: %w", saveErr))
	}
	return state, err
}
