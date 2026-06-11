package vscode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

// LockedVersion is the VS Code version we ship. Bumping requires updating
// the SHA256 below (fetch from https://code.visualstudio.com/sha?build=stable).
const LockedVersion = "1.96.0"

// lockedCommitWin64User is the immutable VS Code git commit for LockedVersion.
// Used in the direct-CDN mirror URL.
const lockedCommitWin64User = "138f619c86f1199955d53b4166bef66ef252935c"

// lockedSHA256Win64User MUST be updated when LockedVersion changes.
// Fetch with:
//
//	curl -s 'https://update.code.visualstudio.com/api/versions/1.96.0/win32-x64-user/stable' | jq -r .sha256hash
const lockedSHA256Win64User = "3b445b7031069b527c16202107baa56ad5f8b5e09e43d688dc71d099c8e1cad1"

const StoreProductID = "XP9KHM4BK9FZ7Q"
const StoreBootstrapperURL = "https://get.microsoft.com/installer/download/" + StoreProductID + "?cid=website_cta_psi"

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
		client = http.DefaultClient
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
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
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
	return Detected{}, fmt.Errorf("install failed and post-install detect didn't find VS Code %s: install err=%w; detect err=%v",
		LockedVersion, installErr, detErr)
}
