package vscode

import (
	"os"
	"strings"
	"testing"
)

func TestLinuxPackageCommonPinsLoomAssets(t *testing.T) {
	body, err := os.ReadFile("../../scripts/linux-package-common.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`LOOM_RELEASE="v0.0.5"`,
		`driver-agent.linux-amd64`,
		`driver-agent.linux-arm64`,
		`slave-agent.linux-amd64`,
		`slave-agent.linux-arm64`,
		`9dd94809801ff71d3e4c26581d48d44796c8e8be28be116b44d02cbd9fcb946c`,
		`1c0a60bfb677a55159dea145dc46ead489b442d2cc55403dd451f3fadec4c7b5`,
		`ce7d0b552a2ee880ef288d14c0d399630b961592fc73e78e98cece7a824ea965`,
		`f7b0740cfb9d9a2c6fa1ad5f015b18c7ee4b3f618fe7082bb00bb828dc683ee6`,
		`driver-skills.tar.gz`,
		`driver-superpower-skills.tar.gz`,
		`driver-codex-prompts.tar.gz`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("linux-package-common.sh missing %q", want)
		}
	}
}

func TestPackageLinuxBuildsBothArchitectures(t *testing.T) {
	body, err := os.ReadFile("../../scripts/package-linux.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`for arch in amd64 arm64`,
		`GOOS=linux GOARCH="$arch"`,
		`./cmd/agentserver`,
		`agentserver-linux-$arch.tar.gz`,
		`codex-manifest-linux-$arch.json`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("package-linux.sh missing %q", want)
		}
	}
}

func TestMakefileExposesLinuxTargets(t *testing.T) {
	body, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`cross-linux:`,
		`package-linux: cross-linux`,
		`bash scripts/package-linux.sh`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Makefile missing %q", want)
		}
	}
}
