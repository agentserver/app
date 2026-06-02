//go:build windows

package env

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestPersistUserEnv_Windows(t *testing.T) {
	const key = "AGENTSERVER_VSCODE_TEST_VAR"
	const val = "hello-windows"
	if err := PersistUserEnv(key, val); err != nil {
		t.Fatalf("persist: %v", err)
	}
	defer func() {
		k, _ := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
		_ = k.DeleteValue(key)
		k.Close()
	}()
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	got, _, err := k.GetStringValue(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != val {
		t.Errorf("got %q want %q", got, val)
	}
}
