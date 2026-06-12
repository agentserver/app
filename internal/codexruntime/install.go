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
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Options struct {
	ManifestPath           string
	DestRoot               string
	CacheDir               string
	Client                 *http.Client
	DownloadAttemptTimeout time.Duration
	ResponseHeaderTimeout  time.Duration
	DownloadIdleTimeout    time.Duration
	VersionCommand         func(context.Context, string) (string, error)
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
		opts.Client = newHTTPClient(opts.responseHeaderTimeout())
	}
	if opts.VersionCommand == nil {
		opts.VersionCommand = runCodexVersion
	}
	codexExe := filepath.Join(opts.DestRoot, filepath.FromSlash(m.CodexExe))
	if runtimeComplete(opts.DestRoot, m.RequiredFiles) {
		versionOutput, err := opts.VersionCommand(ctx, codexExe)
		if err == nil && versionMatchesPinned(versionOutput, m) {
			return InstallResult{
				Version:  m.PinnedVersion,
				Source:   "existing",
				CodexExe: codexExe,
				Skipped:  true,
			}, nil
		}
	}
	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return InstallResult{}, err
	}
	var lastErr error
	for _, candidate := range PinnedCandidates(m) {
		res, err := installCandidate(ctx, opts, m, candidate)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return InstallResult{}, fmt.Errorf("无法从国内 npm 镜像下载 Codex pinned runtime %s: no pinned mirrors configured", m.PinnedVersion)
	}
	return InstallResult{}, fmt.Errorf("无法从国内 npm 镜像下载 Codex pinned runtime %s: %w", m.PinnedVersion, lastErr)
}

func installCandidate(ctx context.Context, opts Options, m Manifest, c PackageCandidate) (InstallResult, error) {
	cachePath := filepath.Join(opts.CacheDir, "codex-"+c.Version+".tgz")
	downloadCtx, cancel := context.WithTimeout(ctx, opts.downloadAttemptTimeout())
	defer cancel()
	if err := downloadPackage(downloadCtx, opts.Client, c.URL, cachePath, opts.downloadIdleTimeout()); err != nil {
		return InstallResult{}, err
	}
	if err := VerifyNPMIntegrity(cachePath, c.Integrity); err != nil {
		return InstallResult{}, fmt.Errorf("codex npm 包校验失败: %w", err)
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
		return InstallResult{}, fmt.Errorf("codex npm 包内容不完整: %w", err)
	}
	codexExe := filepath.Join(opts.DestRoot, filepath.FromSlash(m.CodexExe))
	versionOutput, err := opts.VersionCommand(ctx, codexExe)
	if err != nil {
		return InstallResult{}, fmt.Errorf("codex --version failed after install: %w", err)
	}
	if !versionMatchesPinned(versionOutput, m) {
		return InstallResult{}, fmt.Errorf("codex --version %q does not match pinned runtime %s", strings.TrimSpace(versionOutput), m.PinnedVersion)
	}
	return InstallResult{Version: c.Version, Source: c.Source, CodexExe: codexExe}, nil
}

const (
	defaultDownloadAttemptTimeout = 45 * time.Second
	defaultResponseHeaderTimeout  = 15 * time.Second
	defaultDownloadIdleTimeout    = 30 * time.Second
)

func (opts Options) downloadAttemptTimeout() time.Duration {
	if opts.DownloadAttemptTimeout > 0 {
		return opts.DownloadAttemptTimeout
	}
	return defaultDownloadAttemptTimeout
}

func (opts Options) responseHeaderTimeout() time.Duration {
	if opts.ResponseHeaderTimeout > 0 {
		return opts.ResponseHeaderTimeout
	}
	return defaultResponseHeaderTimeout
}

func (opts Options) downloadIdleTimeout() time.Duration {
	if opts.DownloadIdleTimeout > 0 {
		return opts.DownloadIdleTimeout
	}
	return defaultDownloadIdleTimeout
}

func newHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport := base.Clone()
		transport.ResponseHeaderTimeout = responseHeaderTimeout
		return &http.Client{Transport: transport}
	}
	return &http.Client{Timeout: responseHeaderTimeout}
}

func downloadPackage(ctx context.Context, client *http.Client, url, dst string, idleTimeout time.Duration) error {
	if runtime.GOOS == "windows" {
		if err := downloadPackageWithCurl(ctx, url, dst, idleTimeout); err == nil {
			return nil
		} else if !errors.Is(err, exec.ErrNotFound) {
			return err
		}
	}
	return downloadPackageHTTP(ctx, client, url, dst, idleTimeout)
}

func downloadPackageWithCurl(ctx context.Context, url, dst string, idleTimeout time.Duration) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	_ = os.Remove(tmp)
	speedTime := int(idleTimeout.Seconds())
	if speedTime < 1 {
		speedTime = 1
	}
	args := []string{
		"-fL",
		"-sS",
		"--retry", "2",
		"--retry-delay", "2",
		"--connect-timeout", "15",
		"--speed-time", strconv.Itoa(speedTime),
		"--speed-limit", "1024",
		"-o", tmp,
		"--write-out", "%{http_code}",
		url,
	}
	out, err := exec.CommandContext(ctx, "curl.exe", args...).CombinedOutput()
	status := strings.TrimSpace(string(out))
	if err != nil {
		_ = os.Remove(tmp)
		if status == "404" || status == "410" {
			return unavailableError{err: fmt.Errorf("GET %s: status %s", url, status)}
		}
		return fmt.Errorf("curl GET %s failed: %w (%s)", url, err, strings.TrimSpace(string(out)))
	}
	if status != "" && status[0] != '2' {
		_ = os.Remove(tmp)
		if status == "404" || status == "410" {
			return unavailableError{err: fmt.Errorf("GET %s: status %s", url, status)}
		}
		return fmt.Errorf("curl GET %s: status %s", url, status)
	}
	return os.Rename(tmp, dst)
}

func downloadPackageHTTP(ctx context.Context, client *http.Client, url, dst string, idleTimeout time.Duration) error {
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
	lastNano.Store(time.Now().UnixNano())
	done := make(chan error, 1)
	interval := idleTimeout / 2
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	go func() {
		_, err := io.Copy(progressWriter{dst: dst, lastNano: &lastNano}, src)
		done <- err
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer cancel()

	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			_ = src.Close()
			return ctx.Err()
		case <-ticker.C:
			last := time.Unix(0, lastNano.Load())
			if time.Since(last) > idleTimeout {
				cancel()
				_ = src.Close()
				return fmt.Errorf("download idle timeout after %s", idleTimeout)
			}
		}
	}
}

func runtimeComplete(root string, required []string) bool {
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return false
		}
	}
	return true
}

func runCodexVersion(ctx context.Context, exe string) (string, error) {
	out, err := exec.CommandContext(ctx, exe, "--version").CombinedOutput()
	versionOutput := strings.TrimSpace(string(out))
	if err != nil {
		return versionOutput, fmt.Errorf("%w: %s", err, versionOutput)
	}
	return versionOutput, nil
}

func versionMatchesPinned(versionOutput string, m Manifest) bool {
	versionOutput = strings.TrimSpace(versionOutput)
	expectedCLI := strings.TrimSuffix(m.PinnedVersion, "-"+m.Platform)
	for _, candidate := range []string{m.PinnedVersion, expectedCLI} {
		if versionOutput == candidate {
			return true
		}
	}
	for _, field := range strings.Fields(versionOutput) {
		for _, candidate := range []string{m.PinnedVersion, expectedCLI} {
			if field == candidate {
				return true
			}
		}
	}
	return false
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
