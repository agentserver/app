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
		`LOOM_RELEASE="v0.0.10"`,
		`driver-agent.linux-amd64`,
		`driver-agent.linux-arm64`,
		`slave-agent.linux-amd64`,
		`slave-agent.linux-arm64`,
		`cc9467cde06f32b9dddb87946192bbf5f38f95008d5023836e3076af89734fd3`,
		`15117e8a0326da2ec6a308e2836108f3be498615764a193c4ba6235ecac7d671`,
		`67e6e79144e9e2c3cefdc7d3c0cbd67ebd87c55bd4116fe30b8d939412042910`,
		`fad2c9ea341ad55283638da39dbe0ba4b17a941412d4af8e9aa1a09fd1fcb175`,
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
		`run make cross-linux first`,
		`agentserver-linux-$arch.tar.gz`,
		`codex-manifest-linux-$arch.json`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("package-linux.sh missing %q", want)
		}
	}
}

func TestPackageLinuxConsumesCrossLinuxBuildOutputs(t *testing.T) {
	body, err := os.ReadFile("../../scripts/package-linux.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "go build") {
		t.Fatalf("package-linux.sh should package cross-linux outputs, not rebuild:\n%s", text)
	}
	for _, want := range []string{
		`cp "$OUT/linux/$arch/agentserver" "$stage/agentserver"`,
		`sha256sum "agentserver-linux-$arch.tar.gz" > "agentserver-linux-$arch.tar.gz.sha256"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("package-linux.sh missing %q", want)
		}
	}
}

func TestPackageLinuxCreatesReproducibleArchives(t *testing.T) {
	body, err := os.ReadFile("../../scripts/package-linux.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`--sort=name`,
		`--owner=0`,
		`--group=0`,
		`--numeric-owner`,
		`--mtime=@0`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("package-linux.sh missing reproducible tar option %q", want)
		}
	}
}

func TestPackageLinuxSupportsDryRun(t *testing.T) {
	body, err := os.ReadFile("../../scripts/package-linux.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`DRY_RUN="${DRY_RUN:-0}"`,
		`if [[ "$DRY_RUN" == "1" ]]`,
		`dry-run`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("package-linux.sh missing dry-run support %q", want)
		}
	}
}

func TestPackageLinuxPreflightsBinariesBeforeDownloads(t *testing.T) {
	body, err := os.ReadFile("../../scripts/package-linux.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	preflight := strings.Index(text, "preflight_binaries")
	downloads := strings.Index(text, "download_support_assets")
	if preflight < 0 {
		t.Fatalf("package-linux.sh missing preflight_binaries")
	}
	if downloads < 0 {
		t.Fatalf("package-linux.sh missing download_support_assets")
	}
	if preflight > downloads {
		t.Fatalf("package-linux.sh should define preflight before downloads")
	}
	if !strings.Contains(text, "preflight_binaries\ndownload_support_assets") {
		t.Fatalf("package-linux.sh should call preflight_binaries immediately before download_support_assets")
	}
}

func TestLinuxPackageCommonRetriesAllCurlErrors(t *testing.T) {
	body, err := os.ReadFile("../../scripts/linux-package-common.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `--retry-all-errors`) {
		t.Fatalf("linux-package-common.sh should retry transient curl failures:\n%s", text)
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
		`GOOS=linux GOARCH=$$arch`,
		`./cmd/agentserver`,
		`OUT="$(DIST)" bash scripts/package-linux.sh`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Makefile missing %q", want)
		}
	}
}

func TestMakefileDisablesCGOForCrossLinux(t *testing.T) {
	body, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `CGO_ENABLED=0 GOOS=linux GOARCH=$$arch`) {
		t.Fatalf("Makefile cross-linux should disable CGO:\n%s", text)
	}
}
