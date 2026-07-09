package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPackagingVerifiesCodexDesktopAuthenticodeSignature(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "scripts", "windows-package-common.sh"),
		filepath.Join("..", "..", "packaging", "windows", "ensure-codex-desktop.ps1"),
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			source := string(body)
			for _, want := range []string{"Get-AuthenticodeSignature", "Microsoft Corporation", "X509Chain"} {
				if !strings.Contains(source, want) {
					t.Fatalf("%s should verify Codex Desktop installer signature with %q:\n%s", file, want, source)
				}
			}
		})
	}
}

func TestCodexDesktopSignatureAllowsTimestampedExpiredSignerChain(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "scripts", "windows-package-common.sh"),
		filepath.Join("..", "..", "packaging", "windows", "ensure-codex-desktop.ps1"),
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			source := string(body)
			for _, want := range []string{
				"Get-AuthenticodeSignature",
				"$sig.Status",
				"NotTimeValid",
				"$chainErrors",
				"Where-Object",
			} {
				if !strings.Contains(source, want) {
					t.Fatalf("%s should accept timestamp-valid Authenticode signatures whose signer chain is currently NotTimeValid; missing %q", file, want)
				}
			}
		})
	}
}
