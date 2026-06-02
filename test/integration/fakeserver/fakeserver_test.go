//go:build integration

package fakeserver

import (
	"net/http"
	"testing"
)

func TestStart(t *testing.T) {
	srv := Start()
	defer srv.Close()

	// device auth always returns 200
	resp, err := http.Post(srv.MSURL()+"/api/oauth2/device/auth", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
}
