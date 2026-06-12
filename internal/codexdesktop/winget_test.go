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
		{name: "source", err: errors.New("exit 1"), out: "msstore source was not found", want: "microsoft store source"},
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

func TestClassifyWingetErrorWrapsWingetNotFound(t *testing.T) {
	got := ClassifyWingetError(ErrWingetNotFound, "")
	if !errors.Is(got, ErrWingetNotFound) {
		t.Fatalf("err=%v, want ErrWingetNotFound", got)
	}
}

func TestClassifyWingetErrorDoesNotTreatAnyMsstoreOutputAsSourceFailure(t *testing.T) {
	got := ClassifyWingetError(errors.New("exit 1"), "msstore package lookup returned no matching package")
	if strings.Contains(got.Error(), "microsoft store source") {
		t.Fatalf("err=%v, want generic winget failure", got)
	}
	if !strings.Contains(got.Error(), "winget install Codex") {
		t.Fatalf("err=%v, want generic winget failure", got)
	}
}

func TestClassifyWingetErrorDoesNotTreatPackageNotFoundInSourceAsSourceFailure(t *testing.T) {
	got := ClassifyWingetError(errors.New("exit 1"), "package Codex was not found in source msstore")
	if strings.Contains(got.Error(), "microsoft store source") {
		t.Fatalf("err=%v, want generic winget failure", got)
	}
	if !strings.Contains(got.Error(), "winget install Codex") {
		t.Fatalf("err=%v, want generic winget failure", got)
	}
}
