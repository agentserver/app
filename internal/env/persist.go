// Package env persists user-level environment variables on Windows and
// broadcasts WM_SETTINGCHANGE so already-running processes can refresh.
//
// On non-Windows platforms PersistUserEnv is a stub (returns nil); the v1
// installer is Windows-only.
package env

import "errors"

func PersistUserEnv(key, value string) error {
	if key == "" {
		return errors.New("env.PersistUserEnv: key required")
	}
	return persistUserEnv(key, value)
}

func DeleteUserEnv(key string) error {
	if key == "" {
		return errors.New("env.DeleteUserEnv: key required")
	}
	return deleteUserEnv(key)
}
