//go:build windows

package console

import (
	"errors"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

func instanceProcessBelongsToCurrentUser(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil || exitCode != windowsStillActive {
		return false
	}
	processUser, err := processUserSID(handle)
	if err != nil {
		return false
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	return processUser.Equals(currentUser.User.Sid)
}

func processUserSID(handle windows.Handle) (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, err
		}
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}
