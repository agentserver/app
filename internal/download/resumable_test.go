package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// rangeServer serves body[start:end] for Range requests.
func rangeServer(body []byte, etag string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			w.WriteHeader(200)
			return
		}
		rng := r.Header.Get("Range")
		if rng == "" {
			w.WriteHeader(200)
			w.Write(body)
			return
		}
		// e.g. "bytes=5-"
		var start int
		fmt.Sscanf(rng, "bytes=%d-", &start)
		if start >= len(body) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d",
			start, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[start:])
	})
}

func TestFreshDownload(t *testing.T) {
	body := []byte("hello world hello world hello world")
	srv := httptest.NewServer(rangeServer(body, `"v1"`))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "f.bin")
	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("body mismatch")
	}
}

func TestResumeFromPartial(t *testing.T) {
	body := []byte("AAAAABBBBBCCCCCDDDDD")
	srv := httptest.NewServer(rangeServer(body, `"v1"`))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "f.bin")
	part := dst + ".part"
	meta := dst + ".meta"

	// Seed a partial download (first 10 bytes) and matching meta
	if err := os.WriteFile(part, body[:10], 0o644); err != nil {
		t.Fatal(err)
	}
	m := Meta{URL: srv.URL, ETag: `"v1"`, TotalSize: int64(len(body)), SHA256: sha256hex(body)}
	mb, _ := m.Marshal()
	os.WriteFile(meta, mb, 0o644)

	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("body mismatch after resume: %q", got)
	}
}

func TestETagChangeRestarts(t *testing.T) {
	body := []byte("12345678")
	srv := httptest.NewServer(rangeServer(body, `"v2"`))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "f.bin")
	// Pretend we had a partial from a previous etag
	os.WriteFile(dst+".part", []byte("OLDOLD"), 0o644)
	m := Meta{URL: srv.URL, ETag: `"v1"`, TotalSize: 99, SHA256: "x"}
	mb, _ := m.Marshal()
	os.WriteFile(dst+".meta", mb, 0o644)

	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("etag-change: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("expected fresh download, got %q", got)
	}
}

func TestSHA256MismatchDeletes(t *testing.T) {
	body := []byte("good body")
	srv := httptest.NewServer(rangeServer(body, `"v1"`))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "f.bin")
	err := DownloadResumable(context.Background(), srv.URL, dst, "deadbeef" /*wrong*/, nil)
	if err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("unexpected err: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("dst should not exist on sha256 fail")
	}
}

func TestNoRangeSupport(t *testing.T) {
	body := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Accept-Ranges, always 200 even on Range
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(200)
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "f.bin")
	os.WriteFile(dst+".part", []byte("XYZ"), 0o644)
	m := Meta{URL: srv.URL, TotalSize: int64(len(body)), SHA256: sha256hex(body)}
	mb, _ := m.Marshal()
	os.WriteFile(dst+".meta", mb, 0o644)

	err := DownloadResumable(context.Background(), srv.URL, dst, sha256hex(body), nil)
	if err != nil {
		t.Fatalf("no-range: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(body) {
		t.Errorf("expected truncate+refresh, got %q", got)
	}
}
