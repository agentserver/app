package updater

import (
	"context"
	crypto_sha256 "crypto/sha256"
	encoding_hex "encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSource is fully controllable per call.
type fakeSource struct {
	name          string
	manifest      Manifest
	fetchErr      error
	downloadErr   error
	downloadBytes []byte
	fetchCount    int
	downloadCount int
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) FetchManifest(ctx context.Context) (Manifest, error) {
	f.fetchCount++
	if f.fetchErr != nil {
		return Manifest{}, f.fetchErr
	}
	return f.manifest, nil
}

func (f *fakeSource) DownloadInstaller(ctx context.Context, m Manifest, dst io.Writer, onProgress func(SpeedSample)) error {
	f.downloadCount++
	if f.downloadErr != nil {
		return f.downloadErr
	}
	_, err := dst.Write(f.downloadBytes)
	return err
}

func fakeManifest(t *testing.T, version string, body []byte) Manifest {
	t.Helper()
	sum := crypto_sha256.Sum256(body)
	return Manifest{
		Version: version,
		URL:     "https://" + AssetsHost + "/agentserver-app-" + version + "-setup.exe",
		SHA256:  encoding_hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	}
}

func newFallbackTestService(t *testing.T, sources []Source) *Service {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Service{
		CurrentVersion: "0.0.1",
		CacheDir:       cacheDir,
		State:          NewStateStore(statePath),
		Sources:        sources,
		StartInstaller: func(context.Context, string) error { return nil },
	}
}

func TestServiceCheckPrefersFirstSource(t *testing.T) {
	body := []byte("payload")
	m := fakeManifest(t, "0.0.2", body)
	first := &fakeSource{name: "github", manifest: m}
	second := &fakeSource{name: "cdn", fetchErr: errors.New("should not be called")}
	svc := newFallbackTestService(t, []Source{first, second})

	state, err := svc.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check err=%v", err)
	}
	if state.LastSourceUsed != "github" {
		t.Fatalf("LastSourceUsed=%q", state.LastSourceUsed)
	}
	if state.Status != StatusAvailable {
		t.Fatalf("status=%v", state.Status)
	}
	if second.fetchCount != 0 {
		t.Fatalf("second source called %d times", second.fetchCount)
	}
}

func TestServiceCheckFallsBackOnFirstError(t *testing.T) {
	body := []byte("payload")
	m := fakeManifest(t, "0.0.2", body)
	first := &fakeSource{name: "github", fetchErr: fmt.Errorf("%w: boom", ErrFetchTimeout)}
	second := &fakeSource{name: "cdn", manifest: m}
	svc := newFallbackTestService(t, []Source{first, second})

	state, err := svc.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check err=%v", err)
	}
	if state.LastSourceUsed != "cdn" {
		t.Fatalf("LastSourceUsed=%q", state.LastSourceUsed)
	}
	if len(state.LastFallbacks) != 1 {
		t.Fatalf("expected 1 fallback, got %d", len(state.LastFallbacks))
	}
	fb := state.LastFallbacks[0]
	if fb.Source != "github" || fb.Stage != "manifest" || !strings.Contains(fb.Reason, "timeout") {
		t.Fatalf("bad fallback: %+v", fb)
	}
}

func TestServiceCheckAllSourcesFail(t *testing.T) {
	first := &fakeSource{name: "github", fetchErr: errors.New("no1")}
	second := &fakeSource{name: "cdn", fetchErr: errors.New("no2")}
	svc := newFallbackTestService(t, []Source{first, second})

	state, err := svc.Check(context.Background(), true)
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
	if state.Status != StatusError {
		t.Fatalf("status=%v", state.Status)
	}
	// Regression: total failure must preserve fallback history.
	if len(state.LastFallbacks) != 2 {
		t.Fatalf("LastFallbacks len=%d want 2", len(state.LastFallbacks))
	}
	if state.LastFallbacks[0].Source != "github" || state.LastFallbacks[1].Source != "cdn" {
		t.Fatalf("fallbacks=%+v", state.LastFallbacks)
	}
}

func TestServiceDownloadAndStartFallsBackOnSHA256Mismatch(t *testing.T) {
	body := []byte("payload7") // 8 bytes
	m := fakeManifest(t, "0.0.2", body)
	badBytes := []byte("wrong777") // same length, different content
	first := &fakeSource{name: "github", manifest: m, downloadBytes: badBytes}
	second := &fakeSource{name: "cdn", manifest: m, downloadBytes: body}
	svc := newFallbackTestService(t, []Source{first, second})

	state, err := svc.DownloadAndStart(context.Background(), m)
	if err != nil {
		t.Fatalf("DownloadAndStart err=%v", err)
	}
	if state.LastSourceUsed != "cdn" {
		t.Fatalf("LastSourceUsed=%q", state.LastSourceUsed)
	}
	found := false
	for _, fb := range state.LastFallbacks {
		if fb.Source == "github" && fb.Stage == "verify" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected verify fallback, got %+v", state.LastFallbacks)
	}
}

func TestServiceParentCtxCancellationSkipsFallback(t *testing.T) {
	first := &fakeSource{name: "github", fetchErr: context.Canceled}
	second := &fakeSource{name: "cdn", fetchErr: errors.New("must not be called")}
	svc := newFallbackTestService(t, []Source{first, second})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Check(ctx, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if second.fetchCount != 0 {
		t.Fatal("second source was called after parent cancel")
	}
}

func TestServiceCompatShortcutWhenSourcesNil(t *testing.T) {
	svc := &Service{ManifestURL: "https://" + AssetsHost + "/x.json"}
	got := svc.effectiveSources()
	if len(got) != 1 {
		t.Fatalf("effectiveSources len=%d", len(got))
	}
	if got[0].Name() != "cdn" {
		t.Fatalf("effectiveSources[0]=%q, want cdn", got[0].Name())
	}
}

func TestServiceRollingFallbackBufferCap(t *testing.T) {
	svc := &Service{Now: func() time.Time { return time.Unix(0, 0) }}
	buf := []FallbackRecord{}
	for i := 0; i < 7; i++ {
		buf = svc.appendFallback(buf, "github", "manifest", fmt.Errorf("err-%d", i))
	}
	if len(buf) != 5 {
		t.Fatalf("buffer len=%d want 5", len(buf))
	}
	if !strings.Contains(buf[0].Reason, "err-2") || !strings.Contains(buf[4].Reason, "err-6") {
		t.Fatalf("wrong window: %+v", buf)
	}
}

func TestServicePersistsFallbackHistoryAcrossAttempts(t *testing.T) {
	body := []byte("payload")
	m := fakeManifest(t, "0.0.2", body)
	gh := &fakeSource{name: "github", fetchErr: errors.New("boom")}
	cdn := &fakeSource{name: "cdn", manifest: m}
	svc := newFallbackTestService(t, []Source{gh, cdn})

	// Attempt 1 with automatic=false to force a fresh check.
	s1, err := svc.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	if s1.LastSourceUsed != "cdn" || len(s1.LastFallbacks) != 1 {
		t.Fatalf("attempt 1 state=%+v", s1)
	}
	// Attempt 2: both succeed this time.
	gh.fetchErr = nil
	gh.manifest = m
	s2, err := svc.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("attempt 2: %v", err)
	}
	if s2.LastSourceUsed != "github" {
		t.Fatalf("attempt 2 LastSourceUsed=%q", s2.LastSourceUsed)
	}
	if len(s2.LastFallbacks) != 1 || s2.LastFallbacks[0].Source != "github" {
		t.Fatalf("attempt 2 must retain prior github failure, got %+v", s2.LastFallbacks)
	}
}
