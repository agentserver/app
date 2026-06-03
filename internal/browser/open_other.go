//go:build !windows

package browser

import "os/exec"

func openPlatform(url string) error {
	// Best-effort on dev hosts; Linux has xdg-open, macOS has open.
	for _, prog := range []string{"xdg-open", "open"} {
		if err := exec.Command(prog, url).Start(); err == nil {
			return nil
		}
	}
	return nil
}
