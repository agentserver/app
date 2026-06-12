package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultManifestURL = "https://assets.agent.cs.ac.cn/agentserver-app/windows/latest.json"

type Service struct {
	CurrentVersion string
	ManifestURL    string
	CacheDir       string
	State          *StateStore
	Client         *http.Client
	StartInstaller func(context.Context, string) error
	Now            func() time.Time
	AutoCheckEvery time.Duration
}

func (s Service) Check(ctx context.Context, automatic bool) (State, error) {
	now := s.now()
	prior, _ := s.loadState()
	if automatic && s.AutoCheckEvery > 0 && !prior.LastCheckedAt.IsZero() && now.Sub(prior.LastCheckedAt) < s.AutoCheckEvery {
		prior.CurrentVersion = s.CurrentVersion
		_ = s.saveState(prior)
		return prior, nil
	}

	checking := State{
		CurrentVersion: s.CurrentVersion,
		LastCheckedAt:  prior.LastCheckedAt,
		Status:         StatusChecking,
		Update:         prior.Update,
	}
	_ = s.saveState(checking)

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
		return state, s.saveState(state)
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
	return state, s.saveState(state)
}

func (s Service) DownloadAndStart(ctx context.Context, m Manifest) (State, error) {
	now := s.now()
	if err := m.Validate(); err != nil {
		return s.saveError(now, err)
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
	_ = s.saveState(downloading)

	finalPath, err := installerCachePath(s.CacheDir, m)
	if err != nil {
		return s.saveError(now, err)
	}
	partPath := finalPath + ".part"
	if err := s.downloadInstaller(ctx, m, partPath); err != nil {
		_ = os.Remove(partPath)
		return s.saveError(now, err)
	}
	if err := verifyInstaller(partPath, m); err != nil {
		_ = os.Remove(partPath)
		return s.saveError(now, err)
	}
	_ = os.Remove(finalPath)
	if err := os.Rename(partPath, finalPath); err != nil {
		_ = os.Remove(partPath)
		return s.saveError(now, err)
	}

	start := s.StartInstaller
	if start == nil {
		start = StartInstaller
	}
	if err := start(ctx, finalPath); err != nil {
		return s.saveError(now, err)
	}
	state := State{
		CurrentVersion: s.CurrentVersion,
		Status:         StatusInstallerStarted,
		Update:         availableFromManifest(m),
	}
	return state, s.saveState(state)
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
	resp, err := s.client().Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("fetch manifest: unexpected status %s", resp.Status)
	}
	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (s Service) downloadInstaller(ctx context.Context, m Manifest, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download installer: unexpected status %s", resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
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
	if m.Size > 0 && n != m.Size {
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
	if !strings.EqualFold(filepath.Ext(name), ".exe") {
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

func (s Service) saveError(now time.Time, err error) (State, error) {
	state := State{
		CurrentVersion: s.CurrentVersion,
		LastCheckedAt:  now,
		Status:         StatusError,
		LastError:      err.Error(),
	}
	_ = s.saveState(state)
	return state, err
}
