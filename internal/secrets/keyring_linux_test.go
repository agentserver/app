//go:build linux

package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestSecretToolLookupErrorClassifiesMissingSecretOnly(t *testing.T) {
	if err := secretToolLookupError(nil, errors.New("exit status 1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty lookup failure error=%v, want ErrNotFound", err)
	}

	err := secretToolLookupError([]byte("Cannot autolaunch D-Bus without X11 $DISPLAY\n"), errors.New("exit status 1"))
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("dbus lookup failure should not be ErrNotFound: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "D-Bus") {
		t.Fatalf("dbus lookup failure error=%v, want diagnostic", err)
	}
}
