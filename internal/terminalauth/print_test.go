package terminalauth

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agentserver/agentserver-pkg/internal/oauth"
)

func TestChallengeURLPrefersCompleteVerificationURI(t *testing.T) {
	ch := oauth.DeviceCodeChallenge{
		VerificationURI:         "https://example.test/verify",
		VerificationURIComplete: "https://example.test/verify?user_code=ABCD",
	}
	if got := ChallengeURL(ch); got != ch.VerificationURIComplete {
		t.Fatalf("ChallengeURL=%q", got)
	}
}

func TestChallengeURLFallsBackToVerificationURI(t *testing.T) {
	ch := oauth.DeviceCodeChallenge{
		VerificationURI: " https://example.test/verify ",
	}
	if got := ChallengeURL(ch); got != "https://example.test/verify" {
		t.Fatalf("ChallengeURL=%q", got)
	}
}

func TestPrintChallengeIncludesTitleURLCodeAndQR(t *testing.T) {
	var buf bytes.Buffer
	qrCalled := false
	ch := oauth.DeviceCodeChallenge{
		UserCode:                "ABCD-EFGH",
		VerificationURIComplete: "https://codeapi.cs.ac.cn/oauth2/device/verify?user_code=ABCD-EFGH",
	}

	PrintChallenge(&buf, "Code 登录", ch, func(w interface{ Write([]byte) (int, error) }, url string) {
		qrCalled = true
		if url != ch.VerificationURIComplete {
			t.Fatalf("QR url=%q", url)
		}
		_, _ = w.Write([]byte("[qr]\n"))
	})

	out := buf.String()
	for _, want := range []string{
		"Code 登录",
		"URL: https://codeapi.cs.ac.cn/oauth2/device/verify?user_code=ABCD-EFGH",
		"Code: ABCD-EFGH",
		"[qr]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if !qrCalled {
		t.Fatal("QR writer was not called")
	}
}

func TestPrintChallengeSkipsEmptyURL(t *testing.T) {
	var buf bytes.Buffer
	PrintChallenge(&buf, "Agentserver 登录", oauth.DeviceCodeChallenge{}, nil)
	if buf.String() != "" {
		t.Fatalf("unexpected output:\n%s", buf.String())
	}
}

func TestPrintURLTrimsURLAndCode(t *testing.T) {
	var buf bytes.Buffer
	PrintURL(&buf, "Agentserver 登录", " https://example.test/verify?user_code=ABCD ", " ABCD ", func(w interface{ Write([]byte) (int, error) }, url string) {
		if url != "https://example.test/verify?user_code=ABCD" {
			t.Fatalf("QR url=%q", url)
		}
	})

	out := buf.String()
	for _, want := range []string{
		"URL: https://example.test/verify?user_code=ABCD",
		"Code: ABCD",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, " ABCD ") {
		t.Fatalf("output contains untrimmed code:\n%s", out)
	}
}

func TestDefaultQRSkipsNilWriter(t *testing.T) {
	assertNotPanics(t, func() {
		DefaultQR(nil, "https://example.test")
	})
}

func TestDefaultQRSkipsUnencodableInput(t *testing.T) {
	var buf bytes.Buffer
	assertNotPanics(t, func() {
		DefaultQR(&buf, strings.Repeat("x", 10000))
	})
}

func assertNotPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	fn()
}
