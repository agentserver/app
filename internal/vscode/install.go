package vscode

import (
	"context"
	"fmt"
	"runtime"
)

type InstallPlan struct {
	URL           string
	SHA256        string
	InstallerType string   // "InnoSetup"
	FileExt       string   // ".exe"
	SilentArgs    []string // e.g. ["/VERYSILENT", "/MERGETASKS=!runcode,addtopath"]
}

// LockedVersion is the VS Code version we ship. Bumping requires updating
// the SHA256 below (fetch from https://code.visualstudio.com/sha?build=stable).
const LockedVersion = "1.96.0"

// lockedSHA256Win64User MUST be updated when LockedVersion changes.
// Fetch with:
//
//	curl -s 'https://update.code.visualstudio.com/api/versions/1.96.0/win32-x64-user/stable' | jq -r .sha256hash
const lockedSHA256Win64User = "3b445b7031069b527c16202107baa56ad5f8b5e09e43d688dc71d099c8e1cad1"

func PlanInstall() InstallPlan {
	return planInstallFor(runtime.GOOS, runtime.GOARCH)
}

func planInstallFor(goos, goarch string) InstallPlan {
	if goos != "windows" || goarch != "amd64" {
		panic(fmt.Sprintf("vscode install: unsupported %s/%s in v1", goos, goarch))
	}
	return InstallPlan{
		URL: "https://update.code.visualstudio.com/" + LockedVersion +
			"/win32-x64-user/stable",
		SHA256:        lockedSHA256Win64User,
		InstallerType: "InnoSetup",
		FileExt:       ".exe",
		SilentArgs: []string{
			"/VERYSILENT",
			"/MERGETASKS=!runcode,addtopath",
			"/SUPPRESSMSGBOXES",
			"/NORESTART",
		},
	}
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
	if detErr == nil && det.Installed && det.Version == LockedVersion {
		return det, nil
	}
	return Detected{}, fmt.Errorf("install failed and post-install detect didn't find VS Code %s: install err=%w; detect err=%v",
		LockedVersion, installErr, detErr)
}
