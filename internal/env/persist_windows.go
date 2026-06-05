//go:build windows

package env

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func persistUserEnv(key, value string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`,
		registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(key, value); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return broadcastSettingChange("Environment")
}

func deleteUserEnv(key string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`,
		registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open HKCU\\Environment: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(key); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return broadcastSettingChange("Environment")
}

const (
	HWND_BROADCAST   = uintptr(0xFFFF)
	WM_SETTINGCHANGE = 0x001A
	SMTO_ABORTIFHUNG = 0x0002
)

func broadcastSettingChange(lparam string) error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	sendMessageTimeout := user32.NewProc("SendMessageTimeoutW")
	lp, _ := windows.UTF16PtrFromString(lparam)
	var result uintptr
	r1, _, e1 := sendMessageTimeout.Call(
		HWND_BROADCAST,
		WM_SETTINGCHANGE,
		0,
		uintptr(unsafe.Pointer(lp)),
		SMTO_ABORTIFHUNG,
		5000,
		uintptr(unsafe.Pointer(&result)),
	)
	if r1 == 0 {
		return fmt.Errorf("SendMessageTimeout: %v", e1)
	}
	return nil
}
