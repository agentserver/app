package codexruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"
)

type Options struct {
	ManifestPath        string
	DestRoot            string
	CacheDir            string
	Client              *http.Client
	DownloadIdleTimeout time.Duration
	VersionCommand      func(context.Context, string) error
}

type InstallResult struct {
	Version  string
	Source   string
	CodexExe string
	Skipped  bool
}

func Ensure(ctx context.Context, opts Options) (InstallResult, error) {
	m, err := LoadManifest(opts.ManifestPath)
	if err != nil {
		return InstallResult{}, err
	}
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.VersionCommand == nil {
		opts.VersionCommand = runCodexVersion
	}
	codexExe := filepath.Join(opts.DestRoot, filepath.FromSlash(m.CodexExe))
	if runtimeComplete(opts.DestRoot, m.RequiredFiles) && opts.VersionCommand(ctx, codexExe) == nil {
		return InstallResult{
			Version:  m.PinnedVersion,
			Source:   "existing",
			CodexExe: codexExe,
			Skipped:  true,
		}, nil
	}
	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return InstallResult{}, err
	}
	allPinnedUnavailable := true
	var lastErr error
	for _, candidate := range PinnedCandidates(m) {
		res, err := installCandidate(ctx, opts, m, candidate)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !IsUnavailable(err) {
			allPinnedUnavailable = false
		}
	}
	if allPinnedUnavailable {
		candidate, err := ResolveLatest(ctx, opts.Client, m)
		if err != nil {
			return InstallResult{}, fmt.Errorf("无法从国内 npm 镜像下载 Codex: pinned unavailable: %v; latest failed: %w", lastErr, err)
		}
		return installCandidate(ctx, opts, m, candidate)
	}
	return InstallResult{}, fmt.Errorf("无法从国内 npm 镜像下载 Codex: %w", lastErr)
}

func installCandidate(ctx context.Context, opts Options, m Manifest, c PackageCandidate) (InstallResult, error) {
	cachePath := filepath.Join(opts.CacheDir, "codex-"+c.Version+".tgz")
	if err := downloadPackage(ctx, opts.Client, c.URL, cachePath, opts.downloadIdleTimeout()); err != nil {
		return InstallResult{}, err
	}
	if err := VerifyNPMIntegrity(cachePath, c.Integrity); err != nil {
		return InstallResult{}, fmt.Errorf("Codex npm 包校验失败: %w", err)
	}
	f, err := os.Open(cachePath)
	if err != nil {
		return InstallResult{}, err
	}
	defer f.Close()
	if err := ExtractRuntime(f, opts.DestRoot, ExtractOptions{
		StripPrefix:   m.StripPrefix,
		RequiredFiles: m.RequiredFiles,
	}); err != nil {
		return InstallResult{}, fmt.Errorf("Codex npm 包内容不完整: %w", err)
	}
	codexExe := filepath.Join(opts.DestRoot, filepath.FromSlash(m.CodexExe))
	if err := opts.VersionCommand(ctx, codexExe); err != nil {
		return InstallResult{}, fmt.Errorf("codex --version failed after install: %w", err)
	}
	return InstallResult{Version: c.Version, Source: c.Source, CodexExe: codexExe}, nil
}

const defaultDownloadIdleTimeout = 30 * time.Second

func (opts Options) downloadIdleTimeout() time.Duration {
	if opts.DownloadIdleTimeout > 0 {
		return opts.DownloadIdleTimeout
	}
	return defaultDownloadIdleTimeout
}

func downloadPackage(ctx context.Context, client *http.Client, url, dst string, idleTimeout time.Duration) error {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return unavailableError{err: fmt.Errorf("GET %s: status %d", url, resp.StatusCode)}
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	wrote := false
	defer func() {
		if !wrote {
			_ = os.Remove(tmp)
		}
	}()
	if err := copyWithIdleTimeout(reqCtx, cancel, out, resp.Body, idleTimeout); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	wrote = true
	return os.Rename(tmp, dst)
}

type progressWriter struct {
	dst      io.Writer
	lastNano *atomic.Int64
}

func (w progressWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.lastNano.Store(time.Now().UnixNano())
	}
	return n, err
}

func copyWithIdleTimeout(ctx context.Context, cancel context.CancelFunc, dst io.Writer, src io.ReadCloser, idleTimeout time.Duration) error {
	if idleTimeout <= 0 {
		_, err := io.Copy(dst, src)
		return err
	}

	var lastNano atomic.Int64
	var timedOut atomic.Bool
	lastNano.Store(time.Now().UnixNano())
	done := make(chan struct{})
	interval := idleTimeout / 2
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				_ = src.Close()
				return
			case <-ticker.C:
				last := time.Unix(0, lastNano.Load())
				if time.Since(last) > idleTimeout {
					timedOut.Store(true)
					cancel()
					_ = src.Close()
					return
				}
			}
		}
	}()

	_, err := io.Copy(progressWriter{dst: dst, lastNano: &lastNano}, src)
	close(done)
	if timedOut.Load() {
		return fmt.Errorf("download idle timeout after %s", idleTimeout)
	}
	return err
}

func runtimeComplete(root string, required []string) bool {
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return false
		}
	}
	return true
}

func runCodexVersion(ctx context.Context, exe string) error {
	return exec.CommandContext(ctx, exe, "--version").Run()
}

type unavailableError struct {
	err error
}

func (e unavailableError) Error() string {
	return e.err.Error()
}

func (e unavailableError) Unwrap() error {
	return e.err
}

func IsUnavailable(err error) bool {
	var unavailable unavailableError
	return errors.As(err, &unavailable)
}
