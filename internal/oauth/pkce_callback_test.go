package oauth

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestReservePort_FirstFree(t *testing.T) {
	// Bind two arbitrary low ports first to occupy them, then offer
	// ReservePort a list where the first two are taken.
	occupy := func() (int, net.Listener) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return l.Addr().(*net.TCPAddr).Port, l
	}
	p1, l1 := occupy()
	defer l1.Close()
	p2, l2 := occupy()
	defer l2.Close()

	// Find a third port that's currently free, then close it so it can be re-bound.
	l3, _ := net.Listen("tcp", "127.0.0.1:0")
	p3 := l3.Addr().(*net.TCPAddr).Port
	l3.Close()

	cfg := AuthCodeConfig{Ports: []int{p1, p2, p3}}
	got, ln, err := ReservePort(cfg)
	if err != nil {
		t.Fatalf("ReservePort: %v", err)
	}
	defer ln.Close()
	if got != p3 {
		t.Errorf("got port %d, want %d (p1=%d occupied, p2=%d occupied)", got, p3, p1, p2)
	}
}

func TestReservePort_AllBusy(t *testing.T) {
	l1, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l1.Close()
	p1 := l1.Addr().(*net.TCPAddr).Port

	cfg := AuthCodeConfig{Ports: []int{p1}}
	_, _, err := ReservePort(cfg)
	if !errors.Is(err, ErrAllPortsBusy) {
		t.Errorf("got %v, want ErrAllPortsBusy", err)
	}
}

// Helper for later tests that need a known-free port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// Silence unused for now (used in later tasks).
var _ = fmt.Sprintf
var _ = freePort
