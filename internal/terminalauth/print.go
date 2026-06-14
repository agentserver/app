package terminalauth

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/mdp/qrterminal/v3"
)

type QRWriter func(w interface{ Write([]byte) (int, error) }, url string)

func ChallengeURL(ch oauth.DeviceCodeChallenge) string {
	if strings.TrimSpace(ch.VerificationURIComplete) != "" {
		return strings.TrimSpace(ch.VerificationURIComplete)
	}
	return strings.TrimSpace(ch.VerificationURI)
}

func PrintChallenge(w io.Writer, title string, ch oauth.DeviceCodeChallenge, qr QRWriter) {
	if w == nil {
		return
	}
	url := ChallengeURL(ch)
	if url == "" {
		return
	}
	if strings.TrimSpace(title) != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(title))
	}
	fmt.Fprintf(w, "URL: %s\n", url)
	if strings.TrimSpace(ch.UserCode) != "" {
		fmt.Fprintf(w, "Code: %s\n", strings.TrimSpace(ch.UserCode))
	}
	if qr == nil {
		qr = DefaultQR
	}
	qr(w, url)
}

func PrintURL(w io.Writer, title, rawURL, userCode string, qr QRWriter) {
	PrintChallenge(w, title, oauth.DeviceCodeChallenge{
		UserCode:                strings.TrimSpace(userCode),
		VerificationURIComplete: strings.TrimSpace(rawURL),
	}, qr)
}

func DefaultQR(w interface{ Write([]byte) (int, error) }, url string) {
	if isNilWriter(w) || strings.TrimSpace(url) == "" {
		return
	}
	defer func() {
		_ = recover()
	}()
	qrterminal.GenerateWithConfig(url, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    w,
		BlackChar: qrterminal.BLACK_BLACK,
		WhiteChar: qrterminal.WHITE_WHITE,
		QuietZone: 1,
	})
}

func isNilWriter(w interface{ Write([]byte) (int, error) }) bool {
	if w == nil {
		return true
	}
	v := reflect.ValueOf(w)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
