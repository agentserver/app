package vscode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"
)

type InstallPlan struct {
	// URLs is the ordered list of mirrors to try; first reachable wins.
	// Callers should iterate in order and break on first 200/206 response.
	URLs            []string
	URL             string
	BootstrapperURL string
	StoreProductID  string
	SHA256          string
	InstallerType   string
	FileExt         string
	SilentArgs      []string
}

const StoreProductID = "XP9KHM4BK9FZ7Q"
const StoreBootstrapperURL = "https://get.microsoft.com/installer/download/" + StoreProductID + "?cid=website_cta_psi"

const (
	minBootstrapperSize                int64 = 65536
	defaultBootstrapperAttemptTimeout        = 5 * time.Minute
	defaultBootstrapperResponseTimeout       = 20 * time.Second
	defaultBootstrapperIdleTimeout           = 30 * time.Second
)

var bootstrapperSignatureValidator = validateBootstrapperSignature

func PlanInstall() InstallPlan {
	return planInstallFor(runtime.GOOS, runtime.GOARCH)
}

func planInstallFor(goos, goarch string) InstallPlan {
	if goos != "windows" || goarch != "amd64" {
		panic(fmt.Sprintf("vscode install: unsupported %s/%s in v1", goos, goarch))
	}
	return InstallPlan{
		URLs:            []string{StoreBootstrapperURL},
		URL:             StoreBootstrapperURL,
		BootstrapperURL: StoreBootstrapperURL,
		StoreProductID:  StoreProductID,
		InstallerType:   "MicrosoftStoreBootstrapper",
		FileExt:         ".exe",
	}
}

func DownloadBootstrapper(ctx context.Context, url, dst string, client *http.Client) error {
	if client == nil {
		client = newBootstrapperHTTPClient(defaultBootstrapperResponseTimeout)
	}
	downloadCtx, cancel := context.WithTimeout(ctx, defaultBootstrapperAttemptTimeout)
	defer cancel()
	return downloadBootstrapper(downloadCtx, url, dst, client, defaultBootstrapperIdleTimeout)
}

func downloadBootstrapper(ctx context.Context, url, dst string, client *http.Client, idleTimeout time.Duration) error {
	if client == nil {
		client = newBootstrapperHTTPClient(defaultBootstrapperResponseTimeout)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download VS Code Microsoft Store bootstrapper: status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	promoted := false
	defer func() {
		if !promoted {
			_ = os.Remove(tmp)
		}
	}()
	if err := copyWithIdleTimeout(ctx, out, resp.Body, idleTimeout); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := validateBootstrapperFile(ctx, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	promoted = true
	return nil
}

func newBootstrapperHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport := base.Clone()
		transport.ResponseHeaderTimeout = responseHeaderTimeout
		return &http.Client{Transport: transport}
	}
	return &http.Client{Timeout: responseHeaderTimeout}
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

func copyWithIdleTimeout(ctx context.Context, dst io.Writer, src io.ReadCloser, idleTimeout time.Duration) error {
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
	copyCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, err := io.Copy(progressWriter{dst: dst, lastNano: &lastNano}, src)
		done <- err
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-copyCtx.Done():
			_ = src.Close()
			return copyCtx.Err()
		case <-ticker.C:
			last := time.Unix(0, lastNano.Load())
			if time.Since(last) > idleTimeout {
				_ = src.Close()
				return fmt.Errorf("download idle timeout after %s", idleTimeout)
			}
		}
	}
}

func validateBootstrapperFile(ctx context.Context, path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() < minBootstrapperSize {
		return fmt.Errorf("VS Code Microsoft Store bootstrapper too small: got %d bytes, want at least %d", st.Size(), minBootstrapperSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 2)
	if _, err := io.ReadFull(f, magic); err != nil {
		return err
	}
	if string(magic) != "MZ" {
		return fmt.Errorf("VS Code Microsoft Store bootstrapper missing MZ executable header")
	}
	return bootstrapperSignatureValidator(ctx, path)
}

// SilentInstall runs the downloaded installer with platform-appropriate args.
func SilentInstall(ctx context.Context, downloadedPath string, plan InstallPlan) error {
	return silentInstallPlatform(ctx, downloadedPath, plan)
}

// InstallAndDetect runs SilentInstall and then Detect. If SilentInstall returns
// an error but Detect reports VS Code at the expected version, the install is
// treated as successful.
//
// This works around a class of issues where Windows Inno Setup returns a
// non-zero exit (e.g. STATUS_STACK_BUFFER_OVERRUN 0xc0000409) in
// non-interactive desktop sessions even though the install completed.
// Seen on Windows 11 build 26100 when invoked over SSH.
//
// installFn and detectFn are injected for testability; pass SilentInstall
// and Detect respectively in production.
func InstallAndDetect(
	ctx context.Context,
	downloadedPath string,
	plan InstallPlan,
	installFn func(context.Context, string, InstallPlan) error,
	detectFn func() (Detected, error),
) (Detected, error) {
	installErr := installFn(ctx, downloadedPath, plan)
	det, detErr := detectFn()
	if installErr == nil {
		// Happy path. Detect failure here is the real error.
		if detErr != nil {
			return Detected{}, fmt.Errorf("install ok but detect failed: %w", detErr)
		}
		return det, nil
	}
	// installer reported failure — last chance: did it actually install?
	if detErr == nil && det.Installed && det.Version != "" {
		return det, nil
	}
	return Detected{}, fmt.Errorf("install failed and post-install detect didn't find VS Code: install err=%w; detect err=%v",
		installErr, detErr)
}
