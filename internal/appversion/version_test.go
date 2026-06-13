package appversion

import (
	"os"
	"regexp"
	"testing"
)

func TestVersionIsSemverLike(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty")
	}
	if !regexp.MustCompile(`^v?\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version=%q, want v?MAJOR.MINOR.PATCH", Version)
	}
}

func TestVersionMatchesWindowsInstallerScript(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/installer.iss")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^#define MyAppVersion "([^"]+)"`).FindSubmatch(body)
	if match == nil {
		t.Fatal("packaging/windows/installer.iss missing MyAppVersion define")
	}
	installerVersion := string(match[1])
	if installerVersion != Version {
		t.Fatalf("installer MyAppVersion=%q, appversion.Version=%q", installerVersion, Version)
	}
}
