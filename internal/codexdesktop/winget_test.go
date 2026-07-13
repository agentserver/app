package codexdesktop

import (
	"errors"
	"strings"
	"testing"
)

const retiredCodexStoreProductID = "9NT1" + "R1C2HH7"

func TestWingetInstallArgs(t *testing.T) {
	if CodexStoreProductID != "9PLM9XGG6VKS" {
		t.Fatalf("CodexStoreProductID=%q", CodexStoreProductID)
	}
	got := WingetInstallArgs()
	want := []string{
		"install",
		"--id=9PLM9XGG6VKS",
		"--source=msstore",
		"--exact",
		"--accept-package-agreements",
		"--accept-source-agreements",
		"--disable-interactivity",
	}
	if len(got) != len(want) {
		t.Fatalf("args len=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d]=%q want %q; all=%v", i, got[i], want[i], got)
		}
	}
	if strings.Contains(strings.Join(got, " "), retiredCodexStoreProductID) {
		t.Fatalf("winget args retain retired Store ID: %v", got)
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
		{name: "generic", err: errors.New("exit 7"), out: "plain failure", want: CodexStoreProductID},
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
	if !strings.Contains(got.Error(), CodexStoreProductID) {
		t.Fatalf("err=%v, want generic winget failure", got)
	}
}

func TestClassifyWingetErrorDoesNotTreatPackageNotFoundInSourceAsSourceFailure(t *testing.T) {
	got := ClassifyWingetError(errors.New("exit 1"), "package Codex was not found in source msstore")
	if strings.Contains(got.Error(), "microsoft store source") {
		t.Fatalf("err=%v, want generic winget failure", got)
	}
	if !strings.Contains(got.Error(), CodexStoreProductID) {
		t.Fatalf("err=%v, want generic winget failure", got)
	}
}
