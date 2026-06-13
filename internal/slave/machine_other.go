//go:build !windows

package slave

func isMachineSharingViolation(error) bool {
	return false
}
