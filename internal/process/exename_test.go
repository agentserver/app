package process

import (
	"runtime"
	"testing"
)

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
	// Exercise the exported ExeName too, so the runtime.GOOS delegation is covered
	// and a future refactor that drops/wires it incorrectly is caught.
	if got := ExeName("launcher"); got != exeNameFor(runtime.GOOS, "launcher") {
		t.Errorf("ExeName(\"launcher\")=%q want %q (exeNameFor on %s)", got, exeNameFor(runtime.GOOS, "launcher"), runtime.GOOS)
	}
}
