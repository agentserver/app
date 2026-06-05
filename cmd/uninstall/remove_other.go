//go:build !windows

package main

import "os"

func removeInstallDirLater(dir string) error {
	if dir == "" {
		return nil
	}
	return os.RemoveAll(dir)
}
