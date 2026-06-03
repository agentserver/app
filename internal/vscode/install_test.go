package vscode

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPlanInstall_Windows(t *testing.T) {
	p := planInstallFor("windows", "amd64")
	if p.URL == "" || p.SHA256 == "" {
		t.Errorf("missing URL/sha: %+v", p)
	}
	if p.InstallerType != "InnoSetup" {
		t.Errorf("type %q", p.InstallerType)
	}
	if len(p.SilentArgs) == 0 {
		t.Errorf("silent args empty")
	}
	if len(p.URLs) < 2 {
		t.Errorf("expected at least 2 mirror URLs (prss + update.code), got %v", p.URLs)
	}
	if p.URL != p.URLs[0] {
		t.Errorf("URL should equal URLs[0] for back-compat: got URL=%q URLs[0]=%q", p.URL, p.URLs[0])
	}
	// prss CDN URL should be tried first (fastest in CN per P13.4 measurements)
	if !strings.Contains(p.URLs[0], "prss.microsoft.com") {
		t.Errorf("expected prss URL first, got %q", p.URLs[0])
	}
}

func TestPlanInstall_Unsupported(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for unsupported")
		}
	}()
	_ = planInstallFor("plan9", "amd64")
}

func TestInstallAndDetect_InstallOK_DetectOK(t *testing.T) {
	install := func(context.Context, string, InstallPlan) error { return nil }
	detect := func() (Detected, error) {
		return Detected{Installed: true, Path: "/x/code", Version: LockedVersion}, nil
	}
	det, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if det.Path != "/x/code" {
		t.Errorf("got %+v", det)
	}
}

func TestInstallAndDetect_InstallOK_DetectFails(t *testing.T) {
	install := func(context.Context, string, InstallPlan) error { return nil }
	detect := func() (Detected, error) {
		return Detected{}, errors.New("VS Code not found")
	}
	_, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err == nil {
		t.Fatal("expected error when install ok but detect fails")
	}
	if !strings.Contains(err.Error(), "install ok but detect failed") {
		t.Errorf("wrong err: %v", err)
	}
}

func TestInstallAndDetect_InstallFails_DetectFindsIt(t *testing.T) {
	// This is the Bug #1 scenario: 0xc0000409 spurious exit code.
	install := func(context.Context, string, InstallPlan) error {
		return errors.New("exit status 0xc0000409")
	}
	detect := func() (Detected, error) {
		return Detected{Installed: true, Path: "/x/code", Version: LockedVersion}, nil
	}
	det, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err != nil {
		t.Fatalf("expected fallback success, got: %v", err)
	}
	if det.Path != "/x/code" || det.Version != LockedVersion {
		t.Errorf("got %+v", det)
	}
}

func TestInstallAndDetect_InstallFails_DetectDoesntFindIt(t *testing.T) {
	install := func(context.Context, string, InstallPlan) error {
		return errors.New("ERROR 5: access denied")
	}
	detect := func() (Detected, error) {
		return Detected{}, errors.New("VS Code not found")
	}
	_, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err == nil {
		t.Fatal("expected error when both install and detect fail")
	}
	if !strings.Contains(err.Error(), "ERROR 5: access denied") {
		t.Errorf("install err should be wrapped: %v", err)
	}
}

func TestInstallAndDetect_InstallFails_DetectFindsWrongVersion(t *testing.T) {
	// e.g. user already had VS Code 1.85 installed; install fails for real;
	// don't pretend it succeeded.
	install := func(context.Context, string, InstallPlan) error {
		return errors.New("disk full")
	}
	detect := func() (Detected, error) {
		return Detected{Installed: true, Path: "/x/code", Version: "1.85.0"}, nil
	}
	_, err := InstallAndDetect(context.Background(), "/tmp/x.exe", InstallPlan{}, install, detect)
	if err == nil {
		t.Fatal("expected error when detected version != LockedVersion")
	}
}
