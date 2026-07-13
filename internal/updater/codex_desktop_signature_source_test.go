package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPackagingVerifiesCodexDesktopAuthenticodeSignature(t *testing.T) {
	file := filepath.Join("..", "..", "packaging", "windows", "verify-chatgpt-desktop-installer.ps1")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"Get-AuthenticodeSignature",
		"CertGetNameStringW",
		"2.5.4.10",
		"2.5.4.6",
		"Microsoft Corporation",
		"1.3.6.1.5.5.7.3.3",
		"X509Chain",
		"RevocationMode]::Online",
		"RevocationFlag]::ExcludeRoot",
		"UrlRetrievalTimeout",
		"TimeStamperCertificate",
		"NotTimeValid",
		"$sawNotTimeValid = $false",
		"$sawNotTimeValid = $true",
		"return $sawNotTimeValid",
		"[System.Security.Cryptography.SHA256]::Create()",
		"$sha256.ComputeHash($root.RawData)",
		"[BitConverter]::ToString",
		"847DF6A78497943F27FC72EB93F9A637320A02B561D0A91B09E87A7807ED7C61",
		"DF545BF919A2439C36983B54CDFC903DFA4F37D3996D8D84B4C31EEC6F3C163E",
		"timestampChain.Build",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("%s should enforce the shared Authenticode policy with %q:\n%s", file, want, source)
		}
	}
	for _, forbidden := range []string{
		"RevocationMode]::NoCheck",
		"-match 'O=Microsoft Corporation'",
		"-match \"O=Microsoft Corporation\"",
		"chainSubjects -match",
		"GetCertHashString([System.Security.Cryptography.HashAlgorithmName]::SHA256)",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("%s contains weak Authenticode policy %q:\n%s", file, forbidden, source)
		}
	}
}

func TestWindowsPackagingAuthenticodeVerifierReadsLiteralPath(t *testing.T) {
	file := filepath.Join("..", "..", "packaging", "windows", "verify-chatgpt-desktop-installer.ps1")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if !strings.Contains(source, "Get-AuthenticodeSignature -LiteralPath $Path") {
		t.Errorf("%s must read the installer payload with Get-AuthenticodeSignature -LiteralPath $Path", file)
	}
	if strings.Contains(source, "Get-AuthenticodeSignature -FilePath $Path") {
		t.Errorf("%s must not read the installer payload with Get-AuthenticodeSignature -FilePath $Path", file)
	}
}

func TestCodexDesktopSignaturePolicyIsSharedByBuildAndRuntime(t *testing.T) {
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
			if !strings.Contains(source, "verify-chatgpt-desktop-installer.ps1") {
				t.Fatalf("%s must call the shared ChatGPT installer signature verifier:\n%s", file, source)
			}
			for _, forbidden := range []string{"RevocationMode]::NoCheck", "Get-AuthenticodeSignature -FilePath $Path"} {
				if strings.Contains(source, forbidden) {
					t.Fatalf("%s duplicates or weakens shared signature policy with %q:\n%s", file, forbidden, source)
				}
			}
		})
	}
}
