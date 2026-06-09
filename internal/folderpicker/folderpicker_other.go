//go:build !windows

package folderpicker

import (
	"context"
	"errors"
)

func selectFolder(context.Context) (string, error) {
	return "", errors.New("folder picker is only available on Windows")
}
