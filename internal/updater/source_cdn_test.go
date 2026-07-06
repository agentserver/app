package updater

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func cdnHostURL(path string) string {
	return "https://" + AssetsHost + path
}

func newCDNTestSource() *cdnSource {
	return NewCDNSource(cdnHostURL("/agentserver-app/windows/latest.json"), http.DefaultClient, DefaultSourcePolicy()).(*cdnSource)
}

func TestCDNSourceNameIsCDN(t *testing.T) {
	if got := newCDNTestSource().Name(); got != "cdn" {
		t.Fatalf("Name()=%q, want cdn", got)
	}
}

func TestCDNSourceRejectsInstallerHostSuffixBypass(t *testing.T) {
	err := newCDNTestSource().validateInstallerURL("https://" + AssetsHost + ".evil.example.com/installer.exe")
	if err == nil {
		t.Fatal("expected suffix bypass to be rejected")
	}
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed", err)
	}
}

func TestCDNSourceRejectsInstallerHostUserinfoBypass(t *testing.T) {
	err := newCDNTestSource().validateInstallerURL("https://" + AssetsHost + "@evil.example.com/installer.exe")
	if err == nil {
		t.Fatal("expected userinfo bypass to be rejected")
	}
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed", err)
	}
}

func TestCDNSourceRejectsURLOutsideAssetsHost(t *testing.T) {
	err := newCDNTestSource().validateInstallerURL("https://evil.example.com/installer.exe")
	if err == nil {
		t.Fatal("expected non-AssetsHost URL to be rejected")
	}
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err=%v; want ErrHostNotAllowed", err)
	}
}

func TestCDNSourceAcceptsMixedCaseAssetsHost(t *testing.T) {
	if err := newCDNTestSource().validateInstallerURL("https://" + strings.ToUpper(AssetsHost) + "/installer.exe"); err != nil {
		t.Fatalf("mixed-case host must be accepted: %v", err)
	}
}

func TestCDNSourceAcceptsTrailingDotAssetsHost(t *testing.T) {
	// DNS treats trailing-dot as equivalent; whitelist must too.
	if err := newCDNTestSource().validateInstallerURL("https://" + AssetsHost + "./installer.exe"); err != nil {
		t.Fatalf("trailing-dot host must be accepted: %v", err)
	}
}

func TestCDNSourceAcceptsAssetsHTTPSInstaller(t *testing.T) {
	if err := newCDNTestSource().validateInstallerURL("https://" + AssetsHost + "/agentserver-app/windows/setup.exe"); err != nil {
		t.Fatalf("standard installer URL must be accepted: %v", err)
	}
}

func TestCDNSourceRejectsNonHTTPSInstaller(t *testing.T) {
	err := newCDNTestSource().validateInstallerURL("http://" + AssetsHost + "/setup.exe")
	if err == nil {
		t.Fatal("expected http:// to be rejected")
	}
}
