//go:build !windows && !linux && !darwin

package slave

func init() {
	resolveProcessExe = func(pid int) (string, error) {
		return "", nil
	}
}
