//go:build windows

package slave

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isMachineSharingViolation(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
