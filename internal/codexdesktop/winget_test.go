package codexdesktop

import (
	"errors"
	"strings"
	"testing"
)

func TestWingetInstallArgs(t *testing.T) {
	got := WingetInstallArgs()
	want := []string{"install", "Codex", "-s", "msstore", "--accept-source-agreements", "--accept-package-agreements"}
	if len(got) != len(want) {
		t.Fatalf("args len=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d]=%q want %q; all=%v", i, got[i], want[i], got)
		}
	}
}

func TestClassifyWingetError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		out  string
		want string
	}{
		{name: "missing", err: ErrWingetNotFound, want: "Windows App Installer"},
		{name: "source", err: errors.New("exit 1"), out: "msstore source was not found", want: "Microsoft Store source"},
		{name: "network", err: errors.New("exit 1"), out: "network failure", want: "网络"},
		{name: "generic", err: errors.New("exit 7"), out: "plain failure", want: "winget install Codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyWingetError(tc.err, tc.out)
			if !strings.Contains(got.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", got.Error(), tc.want)
			}
		})
	}
}
