package oauth

import (
	"embed"
	"errors"
	"fmt"
	"net"
)

// ErrAllPortsBusy is returned by ReservePort when none of cfg.Ports could be bound.
var ErrAllPortsBusy = errors.New("all configured callback ports are busy")

// ReservePort tries cfg.Ports in order, returns the first that net.Listen accepts.
// The returned listener is held for StartListening; caller MUST hand it off or .Close() it.
func ReservePort(cfg AuthCodeConfig) (port int, ln net.Listener, err error) {
	for _, p := range cfg.Ports {
		l, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if lerr == nil {
			return p, l, nil
		}
	}
	return 0, nil, ErrAllPortsBusy
}

//go:embed templates/success.html templates/denied.html templates/state_mismatch.html templates/missing_code.html
var callbackTemplates embed.FS

// callbackPage returns the embedded HTML for the named outcome.
// Panics on unknown name (programmer error).
func callbackPage(name string) []byte {
	b, err := callbackTemplates.ReadFile("templates/" + name + ".html")
	if err != nil {
		panic("oauth: missing embedded template " + name + ": " + err.Error())
	}
	return b
}
