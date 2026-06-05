package env

import "testing"

func TestPersistUserEnv_NoEmptyKey(t *testing.T) {
	if err := PersistUserEnv("", "v"); err == nil {
		t.Errorf("expected error for empty key")
	}
}

func TestDeleteUserEnv_NoEmptyKey(t *testing.T) {
	if err := DeleteUserEnv(""); err == nil {
		t.Errorf("expected error for empty key")
	}
}
