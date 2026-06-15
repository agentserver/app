//go:build darwin

package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StartInstaller extracts the downloaded archive (zip or dmg) containing the new
// .app, swaps the running bundle via replaceFile, and relaunches. Mirrors the
// Windows cmd.Start()+Process.Release() "fire and forget" model.
func StartInstaller(ctx context.Context, path string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	installedApp := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // 星池指挥官.app
	appName := filepath.Base(installedApp)

	stage, err := os.MkdirTemp("", "agentserver-update-*")
	if err != nil {
		return err
	}

	var newApp string
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".zip"):
		if out, err := exec.CommandContext(ctx, "unzip", "-o", "-q", path, "-d", stage).CombinedOutput(); err != nil {
			return fmt.Errorf("unzip update: %w: %s", err, out)
		}
		newApp = filepath.Join(stage, appName)
	case strings.HasSuffix(strings.ToLower(path), ".dmg"):
		newApp, err = copyAppFromDMG(ctx, path, stage, appName)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown installer archive: %s", path)
	}

	if err := replaceFile(newApp, installedApp); err != nil {
		return err
	}

	relaunch := filepath.Join(installedApp, "Contents", "MacOS", "launcher")
	cmd := exec.Command(relaunch, "--background")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("relaunch launcher: %w", err)
	}
	return cmd.Process.Release()
}

func copyAppFromDMG(ctx context.Context, dmg, stage, appName string) (string, error) {
	mnt, err := os.MkdirTemp("", "dmg-*")
	if err != nil {
		return "", err
	}
	if out, err := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		_ = os.RemoveAll(mnt)
		return "", fmt.Errorf("hdiutil attach: %w: %s", err, out)
	}
	defer func() {
		_ = exec.Command("hdiutil", "detach", mnt).Run()
		_ = os.RemoveAll(mnt)
	}()
	dst := filepath.Join(stage, appName)
	if out, err := exec.CommandContext(ctx, "cp", "-R", filepath.Join(mnt, appName), dst).CombinedOutput(); err != nil {
		return "", fmt.Errorf("cp app from dmg: %w: %s", err, out)
	}
	return dst, nil
}
