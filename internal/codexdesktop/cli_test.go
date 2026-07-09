package codexdesktop

import "testing"

func TestDefaultCLIPathUsesUserWindowsAppsAliasWhenItExists(t *testing.T) {
	want := `C:\Users\me\AppData\Local\Microsoft\WindowsApps\codex.exe`
	got := defaultCLIPath(`C:\Users\me\AppData\Local`, func(path string) bool {
		return path == want
	})
	if got != want {
		t.Fatalf("defaultCLIPath=%q, want %q", got, want)
	}
}

func TestDefaultCLIPathReturnsEmptyWhenAliasMissing(t *testing.T) {
	got := defaultCLIPath(`C:\Users\me\AppData\Local`, func(string) bool {
		return false
	})
	if got != "" {
		t.Fatalf("defaultCLIPath missing alias=%q, want empty", got)
	}
}

func TestDefaultCLIPathUsesFilesystemCheck(t *testing.T) {
	got := defaultCLIPath(`C:\Users\me\AppData\Local`, func(path string) bool {
		return path == `C:\Users\me\AppData\Local\Microsoft\WindowsApps\codex.exe`
	})
	want := `C:\Users\me\AppData\Local\Microsoft\WindowsApps\codex.exe`
	if got != want {
		t.Fatalf("DefaultCLIPath=%q, want %q", got, want)
	}
}

func TestDefaultCLIPathEmptyLocalAppData(t *testing.T) {
	if got := DefaultCLIPath(""); got != "" {
		t.Fatalf("DefaultCLIPath empty=%q, want empty", got)
	}
}
