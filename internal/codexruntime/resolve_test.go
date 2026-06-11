package codexruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPinnedCandidatesPreferManifestOrder(t *testing.T) {
	m := Manifest{
		PinnedVersion: "0.136.0-win32-x64",
		Pinned: PinnedPackage{
			Integrity: "sha512-pinned",
			Shasum:    "abc123",
			URLs:      []string{"https://mirror1/codex.tgz", "https://mirror2/codex.tgz"},
		},
	}
	got := PinnedCandidates(m)
	if len(got) != 2 {
		t.Fatalf("candidates=%#v", got)
	}
	if got[0].Version != "0.136.0-win32-x64" || got[0].URL != "https://mirror1/codex.tgz" {
		t.Fatalf("first candidate=%#v", got[0])
	}
	if got[1].URL != "https://mirror2/codex.tgz" {
		t.Fatalf("second candidate=%#v", got[1])
	}
}

func TestResolveLatestWindowsPlatformPackage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"optionalDependencies": {
				"@openai/codex-win32-x64": "npm:@openai/codex@0.139.0-win32-x64"
			}
		}`))
	})
	mux.HandleFunc("/pkg/0.139.0-win32-x64", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"dist": {
				"tarball": "https://mirror/codex-0.139.0-win32-x64.tgz",
				"integrity": "sha512-latest",
				"shasum": "def456"
			}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := ResolveLatest(context.Background(), http.DefaultClient, Manifest{
		LatestMetadataURLs:          []string{srv.URL + "/latest"},
		PackageMetadataURLTemplates: []string{srv.URL + "/pkg/{version}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.139.0-win32-x64" {
		t.Fatalf("Version=%q", got.Version)
	}
	if got.URL != "https://mirror/codex-0.139.0-win32-x64.tgz" {
		t.Fatalf("URL=%q", got.URL)
	}
	if got.Integrity != "sha512-latest" {
		t.Fatalf("Integrity=%q", got.Integrity)
	}
}

func TestResolveLatestRejectsMissingIntegrity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"optionalDependencies":{"@openai/codex-win32-x64":"npm:@openai/codex@0.139.0-win32-x64"}}`))
	})
	mux.HandleFunc("/pkg/0.139.0-win32-x64", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"dist":{"tarball":"https://mirror/codex.tgz"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := ResolveLatest(context.Background(), http.DefaultClient, Manifest{
		LatestMetadataURLs:          []string{srv.URL + "/latest"},
		PackageMetadataURLTemplates: []string{srv.URL + "/pkg/{version}"},
	})
	if err == nil || !strings.Contains(err.Error(), "dist.integrity") {
		t.Fatalf("err=%v, want missing dist.integrity", err)
	}
}
