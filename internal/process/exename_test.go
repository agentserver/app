package process

import "testing"

func TestExeName(t *testing.T) {
	tests := []struct {
		goos string
		name string
		want string
	}{
		{"windows", "launcher", "launcher.exe"},
		{"windows", "open-folder", "open-folder.exe"},
		{"darwin", "launcher", "launcher"},
		{"linux", "launcher", "launcher"},
	}
	for _, tt := range tests {
		if got := exeNameFor(tt.goos, tt.name); got != tt.want {
			t.Errorf("exeNameFor(%q,%q)=%q want %q", tt.goos, tt.name, got, tt.want)
		}
	}
}
