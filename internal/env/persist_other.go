//go:build !windows

package env

// On non-Windows v1 builds, this is a no-op so unit tests on Linux pass.
func persistUserEnv(key, value string) error { return nil }
