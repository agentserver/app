//go:build darwin

package codexdesktop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

func downloadToCache(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.MkdirTemp("", "codexdesktop-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, "codex.dmg")
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return out, nil
}

// installDMGApp mounts the dmg, copies appName (.app) into /Applications, detaches.
func installDMGApp(ctx context.Context, dmgPath, appName string) error {
	mnt, err := os.MkdirTemp("", "dmg-*")
	if err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-mountpoint", mnt, dmgPath).CombinedOutput(); err != nil {
		_ = os.RemoveAll(mnt)
		return fmt.Errorf("hdiutil attach: %w: %s", err, out)
	}
	// Detach first (LIFO: runs before RemoveAll), then remove the now-empty
	// mountpoint dir so we don't leak a temp dir per install.
	defer func() {
		_ = exec.Command("hdiutil", "detach", mnt).Run()
		_ = os.RemoveAll(mnt)
	}()
	src := filepath.Join(mnt, appName)
	dst := filepath.Join("/Applications", appName)
	_ = os.RemoveAll(dst)
	if out, err := exec.CommandContext(ctx, "cp", "-R", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("cp %s: %w: %s", appName, err, out)
	}
	return nil
}
