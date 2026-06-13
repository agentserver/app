package appversion

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

const expectedReleaseVersion = "0.1.2"

func TestVersionIsSemverLike(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty")
	}
	if !regexp.MustCompile(`^v?\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version=%q, want v?MAJOR.MINOR.PATCH", Version)
	}
}

func TestVersionIsExpectedRelease(t *testing.T) {
	if Version != expectedReleaseVersion {
		t.Fatalf("Version=%q, want release %q", Version, expectedReleaseVersion)
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

func TestVersionMatchesReleasePackagingSources(t *testing.T) {
	wantString(t, "../../packaging/windows/install.ps1", "$Version    = '"+Version+"'")
	wantString(t, "../../scripts/package-windows.sh", "VERSION=\""+Version+"\"")
	wantString(t, "../../scripts/package-windows-zip.sh", "VERSION=\""+Version+"\"")
	wantString(t, "../../scripts/windows-package-common.sh", "agentserver-app-$VERSION.vsix")
	wantString(t, "../../packaging/windows/installer.iss", "agentserver-app-"+Version+".vsix")

	for _, path := range []string{
		"../../extensions/agentserver-app/package.json",
	} {
		t.Run(path, func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var pkg struct {
				Version string `json:"version"`
			}
			if err := json.Unmarshal(body, &pkg); err != nil {
				t.Fatal(err)
			}
			if pkg.Version != Version {
				t.Fatalf("%s version=%q, appversion.Version=%q", path, pkg.Version, Version)
			}
		})
	}

	t.Run("../../extensions/agentserver-app/package-lock.json", func(t *testing.T) {
		body, err := os.ReadFile("../../extensions/agentserver-app/package-lock.json")
		if err != nil {
			t.Fatal(err)
		}
		var lock struct {
			Version  string `json:"version"`
			Packages map[string]struct {
				Version string `json:"version"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(body, &lock); err != nil {
			t.Fatal(err)
		}
		if lock.Version != Version {
			t.Fatalf("package-lock root version=%q, appversion.Version=%q", lock.Version, Version)
		}
		if lock.Packages[""].Version != Version {
			t.Fatalf("package-lock package version=%q, appversion.Version=%q", lock.Packages[""].Version, Version)
		}
	})
}

func wantString(t *testing.T, path, want string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), want) {
		t.Fatalf("%s missing %q", path, want)
	}
}
