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
	if strings.Contains(buf.String(), "URL:") {
		t.Fatalf("unexpected URL output:\n%s", buf.String())
	}
}
