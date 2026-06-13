package modelserver

import "testing"

func TestDeviceConfigUsesModelserverDeviceWrapper(t *testing.T) {
	got := DeviceConfig("")
	if got.Endpoint != "https://codeapi.cs.ac.cn" {
		t.Fatalf("Endpoint=%q", got.Endpoint)
	}
	if got.AuthPath != "/oauth/device/code" {
		t.Fatalf("AuthPath=%q", got.AuthPath)
	}
	if got.TokenPath != "/oauth/device/token" {
		t.Fatalf("TokenPath=%q", got.TokenPath)
	}
	if got.ClientID != "5321f7e6-3d79-4ac9-a742-04809dbf9025" {
		t.Fatalf("ClientID=%q", got.ClientID)
	}
	if got.Scope != "project:inference offline_access" {
		t.Fatalf("Scope=%q", got.Scope)
	}
}

func TestDeviceConfigAllowsEndpointOverride(t *testing.T) {
	got := DeviceConfig("https://codeapi.test/")
	if got.Endpoint != "https://codeapi.test" {
		t.Fatalf("Endpoint=%q", got.Endpoint)
	}
	if got.AuthURL() != "https://codeapi.test/oauth/device/code" {
		t.Fatalf("AuthURL=%q", got.AuthURL())
	}
	if got.TokenURL() != "https://codeapi.test/oauth/device/token" {
		t.Fatalf("TokenURL=%q", got.TokenURL())
	}
}
