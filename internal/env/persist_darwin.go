//go:build darwin

package env

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// persistUserEnv makes KEY=VALUE visible to new shells and the current GUI
// session: `launchctl setenv` for the current session, plus a managed block in
// ~/.zshrc and ~/.bash_profile for new terminals / reboots.
//
// 已知局限：macOS GUI 应用不继承 rc 文件环境；launchctl setenv 只对当前会话有效。
// 对「把 key 暴露给 slave 子进程」够用（slave 由 launcher 显式 spawn）。
func persistUserEnv(key, value string) error {
	if err := exec.Command("launchctl", "setenv", key, value).Run(); err != nil {
		return fmt.Errorf("launchctl setenv %s: %w", key, err)
	}
	line := fmt.Sprintf("export %s=%q", key, value)
	for _, rc := range []string{".zshrc", ".bash_profile"} {
		if err := writeManagedRC(rc, line); err != nil {
			return err
		}
	}
	return nil
}

func deleteUserEnv(key string) error {
	_ = exec.Command("launchctl", "unsetenv", key).Run()
	for _, rc := range []string{".zshrc", ".bash_profile"} {
		if err := removeManagedRC(rc); err != nil {
			return err
		}
	}
	return nil
}

func writeManagedRC(rcName, line string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, rcName)
	content, _ := os.ReadFile(path)
	updated := injectManagedBlock(string(content), line, managedStartMarker, managedEndMarker)
	return os.WriteFile(path, []byte(updated), 0o644)
}

func removeManagedRC(rcName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, rcName)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	updated := removeManagedBlock(string(content), managedStartMarker, managedEndMarker)
	return os.WriteFile(path, []byte(updated), 0o644)
}
