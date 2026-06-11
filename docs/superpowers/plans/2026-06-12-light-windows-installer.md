# Light Windows Installer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a light Windows installer that does not bundle `codex.exe` or `vscode-installer.exe`, downloads the Codex Windows runtime from domestic npm mirrors during install, and installs minimal VS Code by downloading and running the Microsoft Store bootstrapper.

**Architecture:** Add a focused `internal/codexruntime` package that understands the npm manifest, resolves pinned-or-latest Windows packages, verifies npm integrity, and extracts only the Codex runtime tree. Wire it through `agentctl install-codex`, call it from Windows install scripts, and replace the VS Code locked-installer path with a Store bootstrapper download path. Keep existing onboarding configuration as the owner of VS Code settings, VSIX install, Codex config, and loom driver config.

**Tech Stack:** Go 1.26.3, standard library `net/http`, `archive/tar`, `compress/gzip`, PowerShell 5, Inno Setup, bash packaging scripts.

---

## File Structure

- Create `internal/codexruntime/manifest.go`: manifest structs and JSON loading.
- Create `internal/codexruntime/resolve.go`: pinned package candidate and latest npm metadata resolution.
- Create `internal/codexruntime/integrity.go`: npm `sha512-...` integrity verification.
- Create `internal/codexruntime/extract.go`: safe `.tgz` extraction for `vendor/x86_64-pc-windows-msvc/`.
- Create `internal/codexruntime/install.go`: idempotent runtime ensure flow.
- Create tests beside each file in `internal/codexruntime/*_test.go`.
- Create `packaging/windows/codex-manifest.json`: pinned mirror metadata.
- Create `packaging/windows/ensure-codex.ps1`: install-time PowerShell wrapper around `agentctl install-codex`.
- Modify `cmd/agentctl/main.go`: add `install-codex` subcommand.
- Create `cmd/agentctl/cmd_install_codex.go`: CLI parsing and runtime installer invocation.
- Modify `cmd/agentctl/cmd_test_subcommands.go`: make `test-download-codex` reuse the new runtime installer.
- Modify `cmd/agentctl/wiring_test.go`: reference new runner.
- Modify `internal/vscode/install.go`: replace locked VS Code installer plan with Store bootstrapper plan.
- Modify `internal/vscode/install_windows.go`: keep process execution but run the downloaded bootstrapper.
- Modify `internal/vscode/detect_windows.go`: add WindowsApps aliases.
- Modify `internal/ui/orchestrator_real.go`: use Store bootstrapper in `EnsureVSCode`; use `codexruntime.Ensure` as the defensive Codex fallback instead of GitHub.
- Modify `cmd/launcher/main.go`: pass `codex-manifest.json` path into UI deps.
- Modify `packaging/windows/ensure-vscode.ps1`: download and run Store bootstrapper.
- Modify `packaging/windows/install.ps1`: require/copy/call `ensure-codex.ps1`, stop requiring `codex.exe`, `vscode-installer.exe`, and `vscode-manifest.json`.
- Modify `packaging/windows/installer.iss`: include `ensure-codex.ps1` and `codex-manifest.json`, remove bundled `codex.exe` and VS Code installer, call Codex ensure after machine setup.
- Delete `packaging/windows/vscode-manifest.json`: no longer used by package build or install.
- Modify `scripts/package-windows.sh`: remove Codex exe and VS Code installer prefetch/preflight.
- Modify `scripts/package-windows-zip.sh`: remove Codex exe and VS Code installer staging.
- Modify `internal/vscode/install_test.go`: update packaging text tests and VS Code install plan tests.

---

### Task 1: Codex Runtime Manifest

**Files:**
- Create: `internal/codexruntime/manifest.go`
- Create: `internal/codexruntime/manifest_test.go`
- Create: `packaging/windows/codex-manifest.json`

- [ ] **Step 1: Write the failing manifest tests**

Create `internal/codexruntime/manifest_test.go`:

```go
package codexruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestParsesPinnedMirrorsAndRuntimeFiles(t *testing.T) {
	m, err := LoadManifest(filepath.Join("..", "..", "packaging", "windows", "codex-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Package != "@openai/codex" {
		t.Fatalf("Package=%q", m.Package)
	}
	if m.Platform != "win32-x64" {
		t.Fatalf("Platform=%q", m.Platform)
	}
	if m.PinnedVersion != "0.136.0-win32-x64" {
		t.Fatalf("PinnedVersion=%q", m.PinnedVersion)
	}
	if m.StripPrefix != "vendor/x86_64-pc-windows-msvc/" {
		t.Fatalf("StripPrefix=%q", m.StripPrefix)
	}
	if m.CodexExe != "bin/codex.exe" {
		t.Fatalf("CodexExe=%q", m.CodexExe)
	}
	for _, want := range []string{
		"bin/codex.exe",
		"codex-path/rg.exe",
		"codex-resources/codex-command-runner.exe",
		"codex-resources/codex-windows-sandbox-setup.exe",
	} {
		if !contains(m.RequiredFiles, want) {
			t.Fatalf("required_files missing %q: %#v", want, m.RequiredFiles)
		}
	}
	if !strings.HasPrefix(m.Pinned.Integrity, "sha512-") {
		t.Fatalf("pinned integrity should be npm sha512, got %q", m.Pinned.Integrity)
	}
	if len(m.Pinned.URLs) != 2 {
		t.Fatalf("pinned URLs=%#v", m.Pinned.URLs)
	}
	if !strings.Contains(m.Pinned.URLs[0], "registry.npmmirror.com") {
		t.Fatalf("first mirror should be npmmirror, got %q", m.Pinned.URLs[0])
	}
	if !strings.Contains(m.Pinned.URLs[1], "npmreg.proxy.ustclug.org") {
		t.Fatalf("second mirror should be USTC, got %q", m.Pinned.URLs[1])
	}
	if len(m.LatestMetadataURLs) != 2 {
		t.Fatalf("latest metadata URLs=%#v", m.LatestMetadataURLs)
	}
	if len(m.PackageMetadataURLTemplates) != 2 {
		t.Fatalf("package metadata URL templates=%#v", m.PackageMetadataURLTemplates)
	}
}

func TestLoadManifestRejectsMissingRequiredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-manifest.json")
	if err := os.WriteFile(path, []byte(`{"package":"@openai/codex"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected missing field error")
	}
	for _, want := range []string{"platform", "pinned_version", "strip_prefix", "codex_exe", "required_files", "pinned.urls"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%v, want mention %q", err, want)
		}
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/codexruntime -run 'TestLoadManifest' -v
```

Expected: FAIL because `internal/codexruntime` and `LoadManifest` do not exist, or because `packaging/windows/codex-manifest.json` is missing.

- [ ] **Step 3: Add manifest file and loader**

Create `packaging/windows/codex-manifest.json`:

```json
{
  "package": "@openai/codex",
  "platform": "win32-x64",
  "pinned_version": "0.136.0-win32-x64",
  "strip_prefix": "vendor/x86_64-pc-windows-msvc/",
  "codex_exe": "bin/codex.exe",
  "required_files": [
    "bin/codex.exe",
    "codex-path/rg.exe",
    "codex-resources/codex-command-runner.exe",
    "codex-resources/codex-windows-sandbox-setup.exe"
  ],
  "pinned": {
    "integrity": "sha512-zS6DAmvjdWeAB1CL9KTUMkwzTwfXtxHy8GAtePw2a93jIqawoG07fBxAXuyoHZ3QXQkwEgqBx1zEEh33gdIKAw==",
    "shasum": "b1eddf5e906d5e23a35db293d96e0cc8390e5563",
    "urls": [
      "https://registry.npmmirror.com/@openai/codex/-/codex-0.136.0-win32-x64.tgz",
      "https://npmreg.proxy.ustclug.org/@openai/codex/-/codex-0.136.0-win32-x64.tgz"
    ]
  },
  "latest_metadata_urls": [
    "https://registry.npmmirror.com/@openai%2Fcodex/latest",
    "https://npmreg.proxy.ustclug.org/@openai%2Fcodex/latest"
  ],
  "package_metadata_url_templates": [
    "https://registry.npmmirror.com/@openai%2Fcodex/{version}",
    "https://npmreg.proxy.ustclug.org/@openai%2Fcodex/{version}"
  ]
}
```

Create `internal/codexruntime/manifest.go`:

```go
package codexruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Manifest struct {
	Package                     string        `json:"package"`
	Platform                    string        `json:"platform"`
	PinnedVersion               string        `json:"pinned_version"`
	StripPrefix                 string        `json:"strip_prefix"`
	CodexExe                    string        `json:"codex_exe"`
	RequiredFiles               []string      `json:"required_files"`
	Pinned                      PinnedPackage `json:"pinned"`
	LatestMetadataURLs          []string      `json:"latest_metadata_urls"`
	PackageMetadataURLTemplates []string      `json:"package_metadata_url_templates"`
}

type PinnedPackage struct {
	Integrity string   `json:"integrity"`
	Shasum    string   `json:"shasum"`
	URLs      []string `json:"urls"`
}

func LoadManifest(path string) (Manifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (m Manifest) Validate() error {
	var missing []string
	if strings.TrimSpace(m.Package) == "" {
		missing = append(missing, "package")
	}
	if strings.TrimSpace(m.Platform) == "" {
		missing = append(missing, "platform")
	}
	if strings.TrimSpace(m.PinnedVersion) == "" {
		missing = append(missing, "pinned_version")
	}
	if strings.TrimSpace(m.StripPrefix) == "" {
		missing = append(missing, "strip_prefix")
	}
	if strings.TrimSpace(m.CodexExe) == "" {
		missing = append(missing, "codex_exe")
	}
	if len(m.RequiredFiles) == 0 {
		missing = append(missing, "required_files")
	}
	if len(m.Pinned.URLs) == 0 {
		missing = append(missing, "pinned.urls")
	}
	if strings.TrimSpace(m.Pinned.Integrity) == "" {
		missing = append(missing, "pinned.integrity")
	}
	if len(missing) > 0 {
		return fmt.Errorf("codex manifest missing %s", strings.Join(missing, ", "))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./internal/codexruntime -run 'TestLoadManifest' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexruntime/manifest.go internal/codexruntime/manifest_test.go packaging/windows/codex-manifest.json
git commit -m "feat: add codex runtime manifest"
```

---

### Task 2: NPM Package Resolution, Integrity, and Extraction

**Files:**
- Create: `internal/codexruntime/resolve.go`
- Create: `internal/codexruntime/resolve_test.go`
- Create: `internal/codexruntime/integrity.go`
- Create: `internal/codexruntime/integrity_test.go`
- Create: `internal/codexruntime/extract.go`
- Create: `internal/codexruntime/extract_test.go`

- [ ] **Step 1: Write failing resolver tests**

Create `internal/codexruntime/resolve_test.go`:

```go
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
```

- [ ] **Step 2: Write failing integrity tests**

Create `internal/codexruntime/integrity_test.go`:

```go
package codexruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyNPMIntegrityAcceptsSHA512(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pkg.tgz")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	integrity := "sha512-m3HSJL1i83hdltRq0+o9czGb+8KJDKra4t/3JRlnPKcjI8PZm6XBHXx6zG4UuMXaDEZjR1wuXDre9G9zvN7AQw=="
	if err := VerifyNPMIntegrity(path, integrity); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyNPMIntegrityRejectsMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pkg.tgz")
	if err := os.WriteFile(path, []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifyNPMIntegrity(path, "sha512-m3HSJL1i83hdltRq0+o9czGb+8KJDKra4t/3JRlnPKcjI8PZm6XBHXx6zG4UuMXaDEZjR1wuXDre9G9zvN7AQw==")
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("err=%v, want integrity mismatch", err)
	}
}
```

- [ ] **Step 3: Write failing extraction tests**

Create `internal/codexruntime/extract_test.go` with a helper that builds `.tgz` files in memory:

```go
package codexruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRuntimeStripsVendorPrefix(t *testing.T) {
	tgz := testTarGz(t, map[string]string{
		"package/vendor/x86_64-pc-windows-msvc/bin/codex.exe": "codex",
		"package/vendor/x86_64-pc-windows-msvc/codex-path/rg.exe": "rg",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-command-runner.exe": "runner",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-windows-sandbox-setup.exe": "sandbox",
		"package/README.md": "ignored",
	})
	dir := t.TempDir()
	err := ExtractRuntime(bytes.NewReader(tgz), dir, ExtractOptions{
		StripPrefix: "vendor/x86_64-pc-windows-msvc/",
		RequiredFiles: []string{
			"bin/codex.exe",
			"codex-path/rg.exe",
			"codex-resources/codex-command-runner.exe",
			"codex-resources/codex-windows-sandbox-setup.exe",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "bin", "codex.exe")); err != nil || string(got) != "codex" {
		t.Fatalf("codex.exe=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README should not be extracted, err=%v", err)
	}
}

func TestExtractRuntimeRejectsTraversal(t *testing.T) {
	tgz := testTarGz(t, map[string]string{
		"package/vendor/x86_64-pc-windows-msvc/../escape.exe": "escape",
	})
	err := ExtractRuntime(bytes.NewReader(tgz), t.TempDir(), ExtractOptions{
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		RequiredFiles: []string{"bin/codex.exe"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("err=%v, want unsafe path", err)
	}
}

func TestExtractRuntimeRejectsSymlink(t *testing.T) {
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "package/vendor/x86_64-pc-windows-msvc/bin/codex.exe",
		Typeflag: tar.TypeSymlink,
		Linkname: "../outside.exe",
		Mode:     0o755,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	err := ExtractRuntime(bytes.NewReader(buf.Bytes()), t.TempDir(), ExtractOptions{
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		RequiredFiles: []string{"bin/codex.exe"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported tar entry") {
		t.Fatalf("err=%v, want unsupported tar entry", err)
	}
}

func testTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
```

- [ ] **Step 4: Run tests to verify they fail**

Run:

```bash
go test ./internal/codexruntime -run 'TestPinnedCandidates|TestResolveLatest|TestVerifyNPMIntegrity|TestExtractRuntime' -v
```

Expected: FAIL because resolver, integrity, and extraction functions do not exist.

- [ ] **Step 5: Implement resolver, integrity, and extraction**

Create `internal/codexruntime/resolve.go`:

```go
package codexruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type PackageCandidate struct {
	Version   string
	URL       string
	Integrity string
	Shasum    string
	Source    string
}

func PinnedCandidates(m Manifest) []PackageCandidate {
	out := make([]PackageCandidate, 0, len(m.Pinned.URLs))
	for _, u := range m.Pinned.URLs {
		out = append(out, PackageCandidate{
			Version:   m.PinnedVersion,
			URL:       u,
			Integrity: m.Pinned.Integrity,
			Shasum:    m.Pinned.Shasum,
			Source:    "pinned",
		})
	}
	return out
}

func ResolveLatest(ctx context.Context, client *http.Client, m Manifest) (PackageCandidate, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var lastErr error
	for _, latestURL := range m.LatestMetadataURLs {
		version, err := fetchLatestPlatformVersion(ctx, client, latestURL)
		if err != nil {
			lastErr = err
			continue
		}
		for _, tmpl := range m.PackageMetadataURLTemplates {
			c, err := fetchPackageMetadata(ctx, client, strings.ReplaceAll(tmpl, "{version}", version))
			if err != nil {
				lastErr = err
				continue
			}
			c.Version = version
			c.Source = "latest"
			return c, nil
		}
	}
	if lastErr != nil {
		return PackageCandidate{}, fmt.Errorf("resolve latest codex package: %w", lastErr)
	}
	return PackageCandidate{}, fmt.Errorf("resolve latest codex package: no metadata URLs configured")
}

func fetchLatestPlatformVersion(ctx context.Context, client *http.Client, url string) (string, error) {
	var payload struct {
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := getJSON(ctx, client, url, &payload); err != nil {
		return "", err
	}
	raw := payload.OptionalDependencies["@openai/codex-win32-x64"]
	const prefix = "npm:@openai/codex@"
	if !strings.HasPrefix(raw, prefix) {
		return "", fmt.Errorf("latest metadata missing @openai/codex-win32-x64 npm alias")
	}
	return strings.TrimPrefix(raw, prefix), nil
}

func fetchPackageMetadata(ctx context.Context, client *http.Client, url string) (PackageCandidate, error) {
	var payload struct {
		Dist struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
			Shasum    string `json:"shasum"`
		} `json:"dist"`
	}
	if err := getJSON(ctx, client, url, &payload); err != nil {
		return PackageCandidate{}, err
	}
	if payload.Dist.Tarball == "" {
		return PackageCandidate{}, fmt.Errorf("package metadata missing dist.tarball")
	}
	if payload.Dist.Integrity == "" {
		return PackageCandidate{}, fmt.Errorf("package metadata missing dist.integrity")
	}
	return PackageCandidate{URL: payload.Dist.Tarball, Integrity: payload.Dist.Integrity, Shasum: payload.Dist.Shasum}, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

Create `internal/codexruntime/integrity.go`:

```go
package codexruntime

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

func VerifyNPMIntegrity(path, integrity string) error {
	const prefix = "sha512-"
	if !strings.HasPrefix(integrity, prefix) {
		return fmt.Errorf("unsupported npm integrity %q", integrity)
	}
	want, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(integrity, prefix))
	if err != nil {
		return fmt.Errorf("decode npm integrity: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := h.Sum(nil)
	if string(got) != string(want) {
		return fmt.Errorf("npm integrity mismatch for %s", path)
	}
	return nil
}
```

Create `internal/codexruntime/extract.go`:

```go
package codexruntime

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type ExtractOptions struct {
	StripPrefix   string
	RequiredFiles []string
}

func ExtractRuntime(r io.Reader, destRoot string, opts ExtractOptions) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		rel, ok := stripPackageRuntimePrefix(h.Name, opts.StripPrefix)
		if !ok {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("unsupported tar entry %s type %d", h.Name, h.Typeflag)
		}
		dst, err := safeJoin(destRoot, rel)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp := dst + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
	}
	for _, req := range opts.RequiredFiles {
		dst, err := safeJoin(destRoot, req)
		if err != nil {
			return err
		}
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("Codex npm package missing required file %s: %w", req, err)
		}
	}
	return nil
}

func stripPackageRuntimePrefix(name, stripPrefix string) (string, bool) {
	cleaned := path.Clean(strings.TrimPrefix(name, "package/"))
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	if !strings.HasPrefix(cleaned, stripPrefix) {
		return "", false
	}
	rel := strings.TrimPrefix(cleaned, stripPrefix)
	return rel, rel != "" && rel != "."
}

func safeJoin(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("unsafe runtime path %q", rel)
	}
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", fmt.Errorf("unsafe runtime path %q", rel)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(rootAbs, cleaned)
	if dst != rootAbs && !strings.HasPrefix(dst, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe runtime path %q", rel)
	}
	return dst, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run:

```bash
go test ./internal/codexruntime -run 'TestPinnedCandidates|TestResolveLatest|TestVerifyNPMIntegrity|TestExtractRuntime' -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/codexruntime/resolve.go internal/codexruntime/resolve_test.go internal/codexruntime/integrity.go internal/codexruntime/integrity_test.go internal/codexruntime/extract.go internal/codexruntime/extract_test.go
git commit -m "feat: resolve and extract codex npm runtime"
```

---

### Task 3: Idempotent Codex Runtime Installer

**Files:**
- Create: `internal/codexruntime/install.go`
- Create: `internal/codexruntime/install_test.go`
- Modify: `internal/ui/orchestrator.go`
- Modify: `internal/ui/orchestrator_real.go`
- Modify: `internal/ui/orchestrator_real_test.go`
- Modify: `cmd/launcher/main.go`

- [ ] **Step 1: Write failing installer tests**

Create `internal/codexruntime/install_test.go`:

```go
package codexruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureInstallsPinnedRuntimeFromFirstGoodMirror(t *testing.T) {
	pkg := runtimePackage(t, "codex")
	integrity := npmIntegrity(pkg)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pkg)
	}))
	defer srv.Close()
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: []string{
			"bin/codex.exe",
			"codex-path/rg.exe",
			"codex-resources/codex-command-runner.exe",
			"codex-resources/codex-windows-sandbox-setup.exe",
		},
		Pinned: PinnedPackage{Integrity: integrity, URLs: []string{srv.URL + "/codex.tgz"}},
	})
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.136.0-win32-x64" || res.Source != "pinned" {
		t.Fatalf("result=%+v", res)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "root", "bin", "codex.exe")); err != nil || string(got) != "codex" {
		t.Fatalf("codex.exe=%q err=%v", got, err)
	}
}

func TestEnsureFallsBackToLatestWhenPinnedReturns404(t *testing.T) {
	pkg := runtimePackage(t, "latest-codex")
	integrity := npmIntegrity(pkg)
	mux := http.NewServeMux()
	mux.HandleFunc("/pinned.tgz", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"optionalDependencies":{"@openai/codex-win32-x64":"npm:@openai/codex@0.139.0-win32-x64"}}`))
	})
	mux.HandleFunc("/pkg/0.139.0-win32-x64", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"dist":{"tarball":"%s/latest.tgz","integrity":"%s"}}`, "http://"+r.Host, integrity)
	})
	mux.HandleFunc("/latest.tgz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(pkg)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:                     "@openai/codex",
		Platform:                    "win32-x64",
		PinnedVersion:               "0.136.0-win32-x64",
		StripPrefix:                 "vendor/x86_64-pc-windows-msvc/",
		CodexExe:                    "bin/codex.exe",
		RequiredFiles:               requiredRuntimeFiles(),
		Pinned:                      PinnedPackage{Integrity: "sha512-missing", URLs: []string{srv.URL + "/pinned.tgz"}},
		LatestMetadataURLs:          []string{srv.URL + "/latest"},
		PackageMetadataURLTemplates: []string{srv.URL + "/pkg/{version}"},
	})
	res, err := Ensure(context.Background(), Options{
		ManifestPath:    manifestPath,
		DestRoot:        filepath.Join(dir, "root"),
		CacheDir:        filepath.Join(dir, "cache"),
		VersionCommand:  func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.139.0-win32-x64" || res.Source != "latest" {
		t.Fatalf("result=%+v", res)
	}
}

func TestEnsureSkipsWhenRuntimeAlreadyWorks(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	for _, rel := range requiredRuntimeFiles() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: requiredRuntimeFiles(),
		Pinned:        PinnedPackage{Integrity: "sha512-unused", URLs: []string{"https://unused.invalid/codex.tgz"}},
	})
	calls := 0
	res, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     root,
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			calls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || calls != 1 {
		t.Fatalf("result=%+v calls=%d", res, calls)
	}
}

func TestEnsureRejectsIntegrityMismatch(t *testing.T) {
	pkg := runtimePackage(t, "codex")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pkg)
	}))
	defer srv.Close()
	dir := t.TempDir()
	manifestPath := writeManifest(t, dir, Manifest{
		Package:       "@openai/codex",
		Platform:      "win32-x64",
		PinnedVersion: "0.136.0-win32-x64",
		StripPrefix:   "vendor/x86_64-pc-windows-msvc/",
		CodexExe:      "bin/codex.exe",
		RequiredFiles: requiredRuntimeFiles(),
		Pinned:        PinnedPackage{Integrity: npmIntegrity([]byte("different")), URLs: []string{srv.URL + "/codex.tgz"}},
	})
	_, err := Ensure(context.Background(), Options{
		ManifestPath: manifestPath,
		DestRoot:     filepath.Join(dir, "root"),
		CacheDir:     filepath.Join(dir, "cache"),
		VersionCommand: func(context.Context, string) error {
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("err=%v, want integrity mismatch", err)
	}
}
```

Add helper functions to the same test file:

```go
func writeManifest(t *testing.T, dir string, m Manifest) string {
	t.Helper()
	path := filepath.Join(dir, "codex-manifest.json")
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func requiredRuntimeFiles() []string {
	return []string{
		"bin/codex.exe",
		"codex-path/rg.exe",
		"codex-resources/codex-command-runner.exe",
		"codex-resources/codex-windows-sandbox-setup.exe",
	}
}

func runtimePackage(t *testing.T, codexBody string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"package/vendor/x86_64-pc-windows-msvc/bin/codex.exe": codexBody,
		"package/vendor/x86_64-pc-windows-msvc/codex-path/rg.exe": "rg",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-command-runner.exe": "runner",
		"package/vendor/x86_64-pc-windows-msvc/codex-resources/codex-windows-sandbox-setup.exe": "sandbox",
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func npmIntegrity(body []byte) string {
	sum := sha512.Sum512(body)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/codexruntime -run 'TestEnsure' -v
```

Expected: FAIL because `Ensure`, `Options`, and `InstallResult` do not exist.

- [ ] **Step 3: Implement installer**

Create `internal/codexruntime/install.go`:

```go
package codexruntime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Options struct {
	ManifestPath   string
	DestRoot       string
	CacheDir       string
	Client         *http.Client
	VersionCommand func(context.Context, string) error
}

type InstallResult struct {
	Version  string
	Source   string
	CodexExe string
	Skipped  bool
}

func Ensure(ctx context.Context, opts Options) (InstallResult, error) {
	m, err := LoadManifest(opts.ManifestPath)
	if err != nil {
		return InstallResult{}, err
	}
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	if opts.VersionCommand == nil {
		opts.VersionCommand = runCodexVersion
	}
	codexExe := filepath.Join(opts.DestRoot, filepath.FromSlash(m.CodexExe))
	if runtimeComplete(opts.DestRoot, m.RequiredFiles) && opts.VersionCommand(ctx, codexExe) == nil {
		return InstallResult{Version: m.PinnedVersion, Source: "existing", CodexExe: codexExe, Skipped: true}, nil
	}
	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return InstallResult{}, err
	}
	var unavailable bool
	var lastErr error
	for _, candidate := range PinnedCandidates(m) {
		res, err := installCandidate(ctx, opts, m, candidate)
		if err == nil {
			return res, nil
		}
		if IsUnavailable(err) {
			unavailable = true
			lastErr = err
			continue
		}
		lastErr = err
	}
	if unavailable {
		candidate, err := ResolveLatest(ctx, opts.Client, m)
		if err != nil {
			return InstallResult{}, fmt.Errorf("无法从国内 npm 镜像下载 Codex: pinned unavailable: %v; latest failed: %w", lastErr, err)
		}
		return installCandidate(ctx, opts, m, candidate)
	}
	return InstallResult{}, fmt.Errorf("无法从国内 npm 镜像下载 Codex: %w", lastErr)
}

func installCandidate(ctx context.Context, opts Options, m Manifest, c PackageCandidate) (InstallResult, error) {
	cachePath := filepath.Join(opts.CacheDir, "codex-"+c.Version+".tgz")
	if err := downloadPackage(ctx, opts.Client, c.URL, cachePath); err != nil {
		return InstallResult{}, err
	}
	if err := VerifyNPMIntegrity(cachePath, c.Integrity); err != nil {
		return InstallResult{}, fmt.Errorf("Codex npm 包校验失败: %w", err)
	}
	f, err := os.Open(cachePath)
	if err != nil {
		return InstallResult{}, err
	}
	defer f.Close()
	if err := ExtractRuntime(f, opts.DestRoot, ExtractOptions{
		StripPrefix:   m.StripPrefix,
		RequiredFiles: m.RequiredFiles,
	}); err != nil {
		return InstallResult{}, fmt.Errorf("Codex npm 包内容不完整: %w", err)
	}
	codexExe := filepath.Join(opts.DestRoot, filepath.FromSlash(m.CodexExe))
	if err := opts.VersionCommand(ctx, codexExe); err != nil {
		return InstallResult{}, fmt.Errorf("codex --version failed after install: %w", err)
	}
	return InstallResult{Version: c.Version, Source: c.Source, CodexExe: codexExe}, nil
}

func downloadPackage(ctx context.Context, client *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return unavailableError{fmt.Errorf("GET %s: status %d", url, resp.StatusCode)}
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func runtimeComplete(root string, required []string) bool {
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return false
		}
	}
	return true
}

func runCodexVersion(ctx context.Context, exe string) error {
	cmd := exec.CommandContext(ctx, exe, "--version")
	return cmd.Run()
}

type unavailableError struct{ err error }

func (e unavailableError) Error() string { return e.err.Error() }
func (e unavailableError) Unwrap() error { return e.err }

func IsUnavailable(err error) bool {
	for err != nil {
		if _, ok := err.(unavailableError); ok {
			return true
		}
		err = unwrap(err)
	}
	return false
}

type unwrapper interface{ Unwrap() error }

func unwrap(err error) error {
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	if strings.Contains(err.Error(), "status 404") || strings.Contains(err.Error(), "status 410") {
		return unavailableError{err}
	}
	return nil
}
```

- [ ] **Step 4: Run installer tests to verify they pass**

Run:

```bash
go test ./internal/codexruntime -run 'TestEnsure' -v
```

Expected: PASS.

- [ ] **Step 5: Write failing UI fallback test**

Modify `internal/ui/orchestrator_real_test.go`: replace `TestConfigureVSCodeWritesSettings` server-based GitHub download assertion with a fake Codex runtime installer hook. Add this test near the existing VS Code configure tests:

```go
func TestConfigureVSCodeUsesCodexRuntimeInstallerWhenCodexMissing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses bash stub")
	}
	dir := t.TempDir()
	codeExe := filepath.Join(dir, "code")
	os.WriteFile(codeExe, []byte("#!/bin/bash\nexit 0\n"), 0o755)
	store := state.NewStore(filepath.Join(dir, "state.json"))
	store.Update(func(s *state.State) error {
		s.VSCode.Path = codeExe
		return nil
	})
	vsix := filepath.Join(dir, "stub.vsix")
	os.WriteFile(vsix, []byte("PK\x03\x04stub"), 0o644)
	codexPath := filepath.Join(dir, "agentserver-app", "bin", "codex.exe")
	calls := 0
	r := &realOrchestrator{d: Deps{
		State:             store,
		CodexAbsPath:      codexPath,
		CodexManifestPath: filepath.Join(dir, "codex-manifest.json"),
		CodexRuntimeEnsure: func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
			calls++
			if manifestPath == "" || destRoot == "" || cacheDir == "" {
				return fmt.Errorf("missing codex runtime args")
			}
			if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
				return err
			}
			return os.WriteFile(codexPath, []byte("codex"), 0o755)
		},
		VSCodeUserDataDir: filepath.Join(dir, "data"),
		VSCodeExtDir:      filepath.Join(dir, "ext"),
		EmbeddedVSIXPath:  vsix,
		CodexConfigPath:   filepath.Join(dir, "codex-config.toml"),
	}}
	if err := r.ConfigureVSCode(context.Background()); err != nil {
		t.Fatalf("ConfigureVSCode: %v", err)
	}
	if calls != 1 {
		t.Fatalf("codex runtime ensure calls=%d", calls)
	}
}
```

- [ ] **Step 6: Run UI test to verify it fails**

Run:

```bash
go test ./internal/ui -run TestConfigureVSCodeUsesCodexRuntimeInstallerWhenCodexMissing -v
```

Expected: FAIL because `Deps.CodexManifestPath` and `Deps.CodexRuntimeEnsure` do not exist.

- [ ] **Step 7: Wire Codex runtime fallback into onboarding**

Modify `internal/ui/orchestrator_real.go`:

```go
import "github.com/agentserver/agentserver-pkg/internal/codexruntime"

type Deps struct {
	// existing fields...
	CodexManifestPath string
	CodexRuntimeEnsure func(context.Context, string, string, string) error
}
```

Replace the existing `ConfigureVSCode` block that copies `BundledCodexPath` or downloads from `codexDownloadURL()` with:

```go
if r.d.CodexAbsPath != "" {
	if _, statErr := os.Stat(r.d.CodexAbsPath); os.IsNotExist(statErr) {
		ensure := r.d.CodexRuntimeEnsure
		if ensure == nil {
			ensure = func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
				_, err := codexruntime.Ensure(ctx, codexruntime.Options{
					ManifestPath: manifestPath,
					DestRoot:     destRoot,
					CacheDir:     cacheDir,
				})
				return err
			}
		}
		destRoot := filepath.Dir(filepath.Dir(r.d.CodexAbsPath))
		cacheDir := filepath.Join(destRoot, "cache", "codex")
		if err := ensure(ctx, r.d.CodexManifestPath, destRoot, cacheDir); err != nil {
			return fmt.Errorf("ensure codex runtime: %w", err)
		}
	}
}
```

Modify `cmd/launcher/main.go` where `ui.Deps` is built:

```go
CodexManifestPath: joinExe(installDir, "codex-manifest.json"),
```

- [ ] **Step 8: Run UI test to verify it passes**

Run:

```bash
go test ./internal/ui -run 'TestConfigureVSCodeUsesCodexRuntimeInstallerWhenCodexMissing|TestConfigureVSCodeWritesSettings' -v
```

Expected: PASS after updating the old test to no longer assert direct GitHub download.

- [ ] **Step 9: Commit**

```bash
git add internal/codexruntime/install.go internal/codexruntime/install_test.go internal/ui/orchestrator.go internal/ui/orchestrator_real.go internal/ui/orchestrator_real_test.go cmd/launcher/main.go
git commit -m "feat: install codex runtime from npm mirrors"
```

---

### Task 4: Agentctl Install-Codex Subcommand

**Files:**
- Create: `cmd/agentctl/cmd_install_codex.go`
- Create: `cmd/agentctl/cmd_install_codex_test.go`
- Modify: `cmd/agentctl/main.go`
- Modify: `cmd/agentctl/cmd_test_subcommands.go`
- Modify: `cmd/agentctl/wiring_test.go`

- [ ] **Step 1: Write failing CLI tests**

Create `cmd/agentctl/cmd_install_codex_test.go`:

```go
package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInstallCodexPassesExplicitPaths(t *testing.T) {
	dir := t.TempDir()
	var gotManifest, gotDestRoot, gotCacheDir string
	orig := installCodexRuntime
	installCodexRuntime = func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
		gotManifest, gotDestRoot, gotCacheDir = manifestPath, destRoot, cacheDir
		return nil
	}
	defer func() { installCodexRuntime = orig }()

	err := runInstallCodex([]string{
		"--manifest", filepath.Join(dir, "codex-manifest.json"),
		"--dest-root", filepath.Join(dir, "agentserver-app"),
		"--cache-dir", filepath.Join(dir, "cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotManifest == "" || gotDestRoot == "" || gotCacheDir == "" {
		t.Fatalf("missing args manifest=%q dest=%q cache=%q", gotManifest, gotDestRoot, gotCacheDir)
	}
}

func TestRunInstallCodexRejectsMissingManifest(t *testing.T) {
	err := runInstallCodex([]string{"--dest-root", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("err=%v, want manifest requirement", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./cmd/agentctl -run 'TestRunInstallCodex' -v
```

Expected: FAIL because `runInstallCodex` and `installCodexRuntime` do not exist.

- [ ] **Step 3: Implement subcommand**

Create `cmd/agentctl/cmd_install_codex.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/codexruntime"
	"github.com/agentserver/agentserver-pkg/internal/paths"
)

var installCodexRuntime = func(ctx context.Context, manifestPath, destRoot, cacheDir string) error {
	res, err := codexruntime.Ensure(ctx, codexruntime.Options{
		ManifestPath: manifestPath,
		DestRoot:     destRoot,
		CacheDir:     cacheDir,
	})
	if err != nil {
		return err
	}
	if res.Skipped {
		fmt.Printf("codex runtime already installed at %s\n", res.CodexExe)
		return nil
	}
	fmt.Printf("codex runtime %s installed at %s\n", res.Version, res.CodexExe)
	return nil
}

func runInstallCodex(args []string) error {
	fs := flag.NewFlagSet("install-codex", flag.ContinueOnError)
	manifest := fs.String("manifest", "", "path to codex-manifest.json")
	destRoot := fs.String("dest-root", "", "destination root, usually %LOCALAPPDATA%\\agentserver-app")
	cacheDir := fs.String("cache-dir", "", "download cache directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if *destRoot == "" {
		*destRoot = p.LocalAppDataRoot
	}
	if *cacheDir == "" {
		*cacheDir = filepath.Join(p.LocalAppDataRoot, "cache", "codex")
	}
	if *manifest == "" {
		return fmt.Errorf("install-codex requires --manifest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	return installCodexRuntime(ctx, *manifest, *destRoot, *cacheDir)
}
```

Modify `cmd/agentctl/main.go` switch:

```go
case "install-codex":
	if err := runInstallCodex(os.Args[2:]); err != nil {
		die(err)
	}
```

Update usage:

```text
  agentctl install-codex --manifest <path>
                                  download and install Codex runtime from npm mirrors
```

Modify `cmd/agentctl/cmd_test_subcommands.go` `runTestDownloadCodex` to call `runInstallCodex` with a manifest path next to `agentctl.exe`:

```go
exe, _ := os.Executable()
manifest := filepath.Join(filepath.Dir(exe), "codex-manifest.json")
if err := runInstallCodex([]string{"--manifest", manifest}); err != nil {
	die(err)
}
```

Modify `cmd/agentctl/wiring_test.go`:

```go
_ = runInstallCodex
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
go test ./cmd/agentctl -run 'TestRunInstallCodex|TestRunnersCompile' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/agentctl/cmd_install_codex.go cmd/agentctl/cmd_install_codex_test.go cmd/agentctl/main.go cmd/agentctl/cmd_test_subcommands.go cmd/agentctl/wiring_test.go
git commit -m "feat: add agentctl codex runtime installer"
```

---

### Task 5: VS Code Store Bootstrapper Install

**Files:**
- Modify: `internal/vscode/install.go`
- Modify: `internal/vscode/install_windows.go`
- Modify: `internal/vscode/detect_windows.go`
- Modify: `internal/vscode/install_test.go`
- Modify: `internal/ui/orchestrator_real.go`
- Modify: `cmd/agentctl/cmd_test_subcommands.go`

- [ ] **Step 1: Write failing VS Code install plan tests**

Modify `internal/vscode/install_test.go` by replacing `TestPlanInstall_Windows` and `TestWindowsPackagingManifestMatchesPlanInstall` with:

```go
func TestPlanInstall_WindowsUsesStoreBootstrapper(t *testing.T) {
	p := planInstallFor("windows", "amd64")
	if p.BootstrapperURL == "" {
		t.Fatalf("missing BootstrapperURL: %+v", p)
	}
	if !strings.Contains(p.BootstrapperURL, "get.microsoft.com/installer/download") {
		t.Fatalf("BootstrapperURL=%q", p.BootstrapperURL)
	}
	if p.StoreProductID != "XP9KHM4BK9FZ7Q" {
		t.Fatalf("StoreProductID=%q", p.StoreProductID)
	}
	if p.FileExt != ".exe" {
		t.Fatalf("FileExt=%q", p.FileExt)
	}
	if p.SHA256 != "" {
		t.Fatalf("Store bootstrapper should not use locked VS Code installer sha, got %q", p.SHA256)
	}
}
```

Add a Windows detection source test by making candidate construction testable. Add this test:

```go
func TestWindowsDetectCandidatesIncludeStoreAliases(t *testing.T) {
	got := detectCandidatesWindows(`C:\Users\me\AppData\Local`, `C:\Program Files`, `C:\Program Files (x86)`)
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps\code.exe`,
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps\code.cmd`,
		`C:\Users\me\AppData\Local\Programs\Microsoft VS Code\bin\code.cmd`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("detect candidates missing %q:\n%s", want, joined)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/vscode -run 'TestPlanInstall_WindowsUsesStoreBootstrapper|TestWindowsDetectCandidatesIncludeStoreAliases' -v
```

Expected: FAIL because `InstallPlan` lacks `BootstrapperURL` and `StoreProductID`, and `detectCandidatesWindows` does not exist.

- [ ] **Step 3: Implement Store bootstrapper plan and detection candidates**

Modify `internal/vscode/install.go`:

```go
type InstallPlan struct {
	URLs            []string
	URL             string
	BootstrapperURL string
	StoreProductID  string
	SHA256          string
	InstallerType   string
	FileExt         string
	SilentArgs      []string
}

const StoreProductID = "XP9KHM4BK9FZ7Q"
const StoreBootstrapperURL = "https://get.microsoft.com/installer/download/" + StoreProductID + "?cid=website_cta_psi"

func planInstallFor(goos, goarch string) InstallPlan {
	if goos != "windows" || goarch != "amd64" {
		panic(fmt.Sprintf("vscode install: unsupported %s/%s in v1", goos, goarch))
	}
	return InstallPlan{
		URLs:            []string{StoreBootstrapperURL},
		URL:             StoreBootstrapperURL,
		BootstrapperURL: StoreBootstrapperURL,
		StoreProductID:  StoreProductID,
		InstallerType:   "MicrosoftStoreBootstrapper",
		FileExt:         ".exe",
	}
}
```

Modify `internal/vscode/detect_windows.go`:

```go
func detectCandidatesWindows(localAppData, programFiles, programFilesX86 string) []string {
	var candidates []string
	if localAppData != "" {
		candidates = append(candidates,
			filepath.Join(localAppData, "Microsoft", "WindowsApps", "code.exe"),
			filepath.Join(localAppData, "Microsoft", "WindowsApps", "code.cmd"),
			filepath.Join(localAppData, "Programs", "Microsoft VS Code", "bin", "code.cmd"),
		)
	}
	if programFiles != "" {
		candidates = append(candidates, filepath.Join(programFiles, "Microsoft VS Code", "bin", "code.cmd"))
	}
	if programFilesX86 != "" {
		candidates = append(candidates, filepath.Join(programFilesX86, "Microsoft VS Code", "bin", "code.cmd"))
	}
	return candidates
}
```

Use `detectCandidatesWindows(os.Getenv("LOCALAPPDATA"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"))` inside `detectPlatform`.

- [ ] **Step 4: Write failing bootstrapper download tests**

Add to `internal/vscode/install_test.go`:

```go
func TestDownloadBootstrapperUsesGETBecauseMicrosoftEndpointRejectsHEAD(t *testing.T) {
	body := []byte("bootstrapper")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method=%s", r.Method)
		}
		w.Write(body)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "vscode-store-bootstrapper.exe")
	if err := DownloadBootstrapper(context.Background(), srv.URL, dst, http.DefaultClient); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body=%q", got)
	}
}
```

- [ ] **Step 5: Run bootstrapper test to verify it fails**

Run:

```bash
go test ./internal/vscode -run TestDownloadBootstrapperUsesGETBecauseMicrosoftEndpointRejectsHEAD -v
```

Expected: FAIL because `DownloadBootstrapper` does not exist.

- [ ] **Step 6: Implement bootstrapper download**

Add to `internal/vscode/install.go`:

```go
func DownloadBootstrapper(ctx context.Context, url, dst string, client *http.Client) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download VS Code Microsoft Store bootstrapper: status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
```

Add imports: `io`, `net/http`, `os`, `path/filepath`.

- [ ] **Step 7: Update onboarding and test subcommand to use bootstrapper**

Modify `internal/ui/orchestrator_real.go` `EnsureVSCode` missing-detection branch:

```go
plan := vscode.PlanInstall()
cache := filepath.Join(r.d.VSCodeUserDataDir, "..", "cache", "vscode-store-bootstrapper"+plan.FileExt)
if err := vscode.DownloadBootstrapper(ctx, plan.BootstrapperURL, cache, nil); err != nil {
	return fmt.Errorf("download VS Code Microsoft Store bootstrapper: %w", err)
}
det2, err := vscode.InstallAndDetect(ctx, cache, plan, vscode.SilentInstall, vscode.Detect)
```

Modify `cmd/agentctl/cmd_test_subcommands.go` `runTestInstallVSCode` to print and download the bootstrapper:

```go
fmt.Printf("Downloading VS Code Microsoft Store bootstrapper from %s ...\n", plan.BootstrapperURL)
if err := vscode.DownloadBootstrapper(ctx, plan.BootstrapperURL, cache, nil); err != nil {
	die(fmt.Errorf("download bootstrapper: %w", err))
}
fmt.Println("Download done, running Microsoft Store bootstrapper...")
```

- [ ] **Step 8: Run VS Code tests**

Run:

```bash
go test ./internal/vscode ./internal/ui ./cmd/agentctl -run 'TestPlanInstall_WindowsUsesStoreBootstrapper|TestWindowsDetectCandidatesIncludeStoreAliases|TestDownloadBootstrapperUsesGETBecauseMicrosoftEndpointRejectsHEAD|TestEnsureVSCode_AlreadyInstalled|TestRecordTestVSCodeInstallSetsMinimalMode' -v
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/vscode/install.go internal/vscode/install_windows.go internal/vscode/detect_windows.go internal/vscode/install_test.go internal/ui/orchestrator_real.go cmd/agentctl/cmd_test_subcommands.go
git commit -m "feat: install vscode through store bootstrapper"
```

---

### Task 6: Windows Install Helper Scripts

**Files:**
- Create: `packaging/windows/ensure-codex.ps1`
- Modify: `packaging/windows/ensure-vscode.ps1`
- Modify: `internal/vscode/install_test.go`

- [ ] **Step 1: Write failing packaging text tests**

Modify `internal/vscode/install_test.go`:

Replace the `ensure-vscode.ps1` expected strings in `TestWindowsInstallScriptsIncludeVSCodeInstaller` with:

```go
want: []string{
	"BootstrapperURL",
	"XP9KHM4BK9FZ7Q",
	"get.microsoft.com/installer/download",
	"vscode-store-bootstrapper.exe",
	"DownloadBootstrapper",
	"Start-Process",
	"Wait-ProcessWithProgress",
	"Get-VSCodeDetection",
	"Set-ScriptOutputEncoding",
},
```

Add a new test:

```go
func TestWindowsEnsureCodexScriptCallsAgentctlInstallCodex(t *testing.T) {
	body, err := os.ReadFile("../../packaging/windows/ensure-codex.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"param(",
		"ManifestPath",
		"AgentctlPath",
		"install-codex",
		"--manifest",
		"--dest-root",
		"--cache-dir",
		"agentserver-app\\cache\\codex",
		"Set-ScriptOutputEncoding",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("ensure-codex.ps1 missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/vscode -run 'TestWindowsInstallScriptsIncludeVSCodeInstaller|TestWindowsEnsureCodexScriptCallsAgentctlInstallCodex' -v
```

Expected: FAIL because `ensure-codex.ps1` is missing and `ensure-vscode.ps1` still contains locked-installer download logic.

- [ ] **Step 3: Create ensure-codex.ps1**

Create `packaging/windows/ensure-codex.ps1`:

```powershell
﻿param(
    [string]$ManifestPath = (Join-Path $PSScriptRoot 'codex-manifest.json'),
    [string]$AgentctlPath = (Join-Path $PSScriptRoot 'agentctl.exe')
)

$ErrorActionPreference = 'Stop'

function Set-ScriptOutputEncoding {
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [Console]::OutputEncoding = $utf8
        $script:OutputEncoding = $utf8
        & chcp.com 65001 > $null 2>$null
    } catch {
    }
}

function Write-Step([string]$Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

Set-ScriptOutputEncoding

if (-not (Test-Path -LiteralPath $AgentctlPath)) {
    throw "agentctl.exe not found: $AgentctlPath"
}
if (-not (Test-Path -LiteralPath $ManifestPath)) {
    throw "codex-manifest.json not found: $ManifestPath"
}

$localRoot = Join-Path $env:LOCALAPPDATA 'agentserver-app'
$cacheDir = Join-Path $localRoot 'cache\codex'
Write-Step "Ensuring Codex runtime from domestic npm mirrors..."
& $AgentctlPath install-codex --manifest $ManifestPath --dest-root $localRoot --cache-dir $cacheDir
if ($LASTEXITCODE -ne 0) {
    throw "agentctl install-codex failed with exit code $LASTEXITCODE"
}
Write-Step "Codex runtime is ready."
```

- [ ] **Step 4: Rewrite ensure-vscode.ps1 to download Store bootstrapper**

Replace `packaging/windows/ensure-vscode.ps1` with a UTF-8 BOM PowerShell script that keeps detection functions and uses the bootstrapper:

```powershell
﻿param(
    [string]$BootstrapperURL = 'https://get.microsoft.com/installer/download/XP9KHM4BK9FZ7Q?cid=website_cta_psi',
    [string]$LocalBootstrapperPath = (Join-Path $env:LOCALAPPDATA 'agentserver-app\cache\vscode\vscode-store-bootstrapper.exe'),
    [int]$InstallTimeoutSeconds = 600
)

$ErrorActionPreference = 'Stop'

function Set-ScriptOutputEncoding {
    try {
        $utf8 = New-Object System.Text.UTF8Encoding $false
        [Console]::OutputEncoding = $utf8
        $script:OutputEncoding = $utf8
        & chcp.com 65001 > $null 2>$null
    } catch {
    }
}

function Write-Step($msg) {
    Write-Host "==> $msg" -ForegroundColor Cyan
}

function Wait-ProcessWithProgress([System.Diagnostics.Process]$Process, [string]$Activity, [string]$Status) {
    $percent = 0
    try {
        while (-not $Process.HasExited) {
            Write-Progress -Activity $Activity -Status $Status -PercentComplete $percent
            Start-Sleep -Milliseconds 500
            $percent += 3
            if ($percent -gt 95) { $percent = 5 }
            $Process.Refresh()
        }
    } finally {
        Write-Progress -Activity $Activity -Completed
    }
}

function Get-VSCodeCommandPath {
    $candidates = @()
    $cmd = Get-Command code.cmd -ErrorAction SilentlyContinue
    if ($cmd) { $candidates += $cmd.Source }
    $cmdExe = Get-Command code.exe -ErrorAction SilentlyContinue
    if ($cmdExe) { $candidates += $cmdExe.Source }
    if ($env:LOCALAPPDATA) {
        $candidates += (Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps\code.exe')
        $candidates += (Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps\code.cmd')
        $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\Microsoft VS Code\bin\code.cmd')
    }
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles 'Microsoft VS Code\bin\code.cmd')
    }
    if (${env:ProgramFiles(x86)}) {
        $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Microsoft VS Code\bin\code.cmd')
    }
    foreach ($p in ($candidates | Where-Object { $_ } | Select-Object -Unique)) {
        if (Test-Path $p) { return $p }
    }
    return $null
}

function Get-VSCodeVersion([string]$CodePath) {
    if (-not $CodePath) { return $null }
    try {
        $out = & $CodePath --version 2>$null
        foreach ($line in $out) {
            $v = "$line".Trim()
            if ($v) { return $v }
        }
    } catch {
        return $null
    }
    return $null
}

function Get-VSCodeDetection {
    $path = Get-VSCodeCommandPath
    $version = Get-VSCodeVersion $path
    return [PSCustomObject]@{ Path = $path; Version = $version }
}

function DownloadBootstrapper {
    $dir = Split-Path -Parent $LocalBootstrapperPath
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
    Write-Step "Downloading VS Code Microsoft Store bootstrapper..."
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($curl) {
        & curl.exe -fL --retry 2 --retry-delay 2 --connect-timeout 20 -o $LocalBootstrapperPath $BootstrapperURL
        if ($LASTEXITCODE -eq 0 -and (Test-Path $LocalBootstrapperPath)) { return }
    }
    Invoke-WebRequest -Uri $BootstrapperURL -OutFile $LocalBootstrapperPath -UseBasicParsing
}

function Wait-ForVSCode([int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        $det = Get-VSCodeDetection
        if ($det.Path -and $det.Version) {
            return $det
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    return Get-VSCodeDetection
}

Set-ScriptOutputEncoding
Write-Step "Checking for VS Code..."
$existing = Get-VSCodeDetection
if ($existing.Path -and $existing.Version) {
    Write-Step "Detected existing VS Code $($existing.Version) at $($existing.Path); skipping install."
    exit 0
}

DownloadBootstrapper
Write-Step "Running VS Code Microsoft Store bootstrapper..."
$proc = Start-Process -FilePath $LocalBootstrapperPath -PassThru
Wait-ProcessWithProgress $proc "Installing VS Code" "正在通过微软商店引导器安装 VS Code，请稍候..."
if ($proc.ExitCode -ne 0) {
    throw "VS Code 微软商店引导器安装失败，退出码 $($proc.ExitCode)"
}
$det = Wait-ForVSCode $InstallTimeoutSeconds
if (-not ($det.Path -and $det.Version)) {
    throw "VS Code 微软商店引导器已退出，但未检测到 code 命令。已检查 WindowsApps 与常规安装目录。"
}
Write-Step "VS Code $($det.Version) installed at $($det.Path)"
```

- [ ] **Step 5: Run tests to verify they pass**

Run:

```bash
go test ./internal/vscode -run 'TestWindowsInstallScriptsIncludeVSCodeInstaller|TestWindowsEnsureCodexScriptCallsAgentctlInstallCodex' -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add packaging/windows/ensure-codex.ps1 packaging/windows/ensure-vscode.ps1 internal/vscode/install_test.go
git commit -m "feat: add windows codex and vscode ensure scripts"
```

---

### Task 7: Windows Packaging Without Codex.exe or VS Code Installer

**Files:**
- Modify: `packaging/windows/installer.iss`
- Modify: `packaging/windows/install.ps1`
- Delete: `packaging/windows/vscode-manifest.json`
- Modify: `scripts/package-windows.sh`
- Modify: `scripts/package-windows-zip.sh`
- Modify: `internal/vscode/install_test.go`

- [ ] **Step 1: Write failing packaging tests**

Modify `internal/vscode/install_test.go`:

Replace old positive assertions for `codex-x86_64-pc-windows-msvc.exe`, `VSCodeUserSetup`, `vscode-installer.exe`, `CODEX_CACHE`, `VSCODE_CACHE`, and `vscode-manifest.json` with negative assertions:

```go
func TestWindowsPackagingDoesNotBundleCodexExeOrVSCodeInstaller(t *testing.T) {
	for _, path := range []string{
		"../../packaging/windows/installer.iss",
		"../../packaging/windows/install.ps1",
		"../../scripts/package-windows.sh",
		"../../scripts/package-windows-zip.sh",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		for _, notWant := range []string{
			"codex-x86_64-pc-windows-msvc.exe",
			"VSCodeUserSetup",
			"vscode-installer.exe",
			"CODEX_CACHE",
			"VSCODE_CACHE",
		} {
			if strings.Contains(s, notWant) {
				t.Fatalf("%s must not contain %q", path, notWant)
			}
		}
	}
}

func TestWindowsPackagingIncludesCodexRuntimeEnsure(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{
			path: "../../packaging/windows/installer.iss",
			want: []string{
				"ensure-codex.ps1",
				"codex-manifest.json",
				"RunEstimatedPowerShellStep('codex-runtime'",
				"ensure-codex.ps1",
				"RunEstimatedPowerShellStep('codex-mode'",
				"RunEstimatedPowerShellStep('vscode-mode'",
			},
		},
		{
			path: "../../packaging/windows/install.ps1",
			want: []string{
				"'ensure-codex.ps1'",
				"'codex-manifest.json'",
				"Ensuring Codex runtime",
				"install-mode.json",
			},
		},
		{
			path: "../../scripts/package-windows.sh",
			want: []string{
				"packaging/windows/ensure-codex.ps1",
				"packaging/windows/codex-manifest.json",
			},
		},
		{
			path: "../../scripts/package-windows-zip.sh",
			want: []string{
				"cp packaging/windows/ensure-codex.ps1",
				"cp packaging/windows/codex-manifest.json",
			},
		},
	} {
		body, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s missing %q", tc.path, want)
			}
		}
	}
}
```

Remove or replace the old tests named:

- `TestWindowsPortableMinimalVSCodeUsesBundledInstaller`
- `TestWindowsPortableInstallerStagesBundledCodexForAllModesBeforeFrontend`
- `TestWindowsInnoInstallerStagesBundledCodexForAllModesBeforeFrontend`
- any `vscode-manifest.json` plan-matching test.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/vscode -run 'TestWindowsPackagingDoesNotBundleCodexExeOrVSCodeInstaller|TestWindowsPackagingIncludesCodexRuntimeEnsure' -v
```

Expected: FAIL because packaging scripts still bundle the old payloads and do not include `ensure-codex.ps1`.

- [ ] **Step 3: Update Inno installer**

Modify `packaging/windows/installer.iss`:

Remove `[Files]` entries for:

```text
dist\cache\rust-v0.136.0\codex-x86_64-pc-windows-msvc.exe
dist\cache\vscode\1.96.0\VSCodeUserSetup-x64-1.96.0.exe
vscode-manifest.json
```

Add:

```pascal
Source: "ensure-codex.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "codex-manifest.json"; DestDir: "{app}"; Flags: ignoreversion
```

Delete `StageBundledCodexForLocalSlaves`.

In `CurStepChanged(ssPostInstall)`, after the `machine.ps1` step and before mode writing, add:

```pascal
RunEstimatedPowerShellStep('codex-runtime', '正在从国内 npm 镜像准备 Codex 运行时...', 'ensure-codex.ps1',
  '-ManifestPath ' + PowerShellQuote(ExpandConstant('{app}\codex-manifest.json')), 300);
```

Update the minimal VS Code install call to remove `-ManifestPath`:

```pascal
RunEstimatedPowerShellStep('vscode-install', '正在安装极简 VS Code（可能需要几分钟，请勿关闭）...', 'ensure-vscode.ps1',
  '', 300);
```

- [ ] **Step 4: Update portable installer**

Modify `packaging/windows/install.ps1`:

In `$required`, remove:

```powershell
'codex.exe',
'vscode-installer.exe',
'vscode-manifest.json',
```

Add:

```powershell
'ensure-codex.ps1',
'codex-manifest.json',
```

Remove the block that copies bundled `codex.exe`.

After machine initialization and before frontend mode setup, add:

```powershell
Write-Step "Ensuring Codex runtime..."
& (Join-Path $InstallDir 'ensure-codex.ps1') -ManifestPath (Join-Path $InstallDir 'codex-manifest.json') -AgentctlPath (Join-Path $InstallDir 'agentctl.exe')
```

Update minimal VS Code call:

```powershell
& (Join-Path $InstallDir 'ensure-vscode.ps1')
```

- [ ] **Step 5: Update package scripts**

Modify `scripts/package-windows.sh`:

- Remove `CODEX_RELEASE`, `CODEX_ASSET`, `CODEX_URL`, `CODEX_CACHE`.
- Remove `VSCODE_MANIFEST`, `VSCODE_CACHE`, `verify_vscode_cache`, `download_vscode_installer`, and VS Code cache download blocks.
- Keep Codex Desktop and loom downloads.
- In preflight, replace `packaging/windows/vscode-manifest.json`, `"$VSCODE_CACHE"`, and `"$CODEX_CACHE"` with:

```bash
packaging/windows/ensure-codex.ps1 \
packaging/windows/codex-manifest.json \
```

Modify `scripts/package-windows-zip.sh` the same way and stage:

```bash
cp packaging/windows/ensure-codex.ps1 "$STAGE/"
cp packaging/windows/codex-manifest.json "$STAGE/"
```

Remove:

```bash
cp "$CODEX_CACHE" "$STAGE/codex.exe"
cp "$VSCODE_CACHE" "$STAGE/vscode-installer.exe"
cp packaging/windows/vscode-manifest.json "$STAGE/"
```

Update README text to say Codex runtime and minimal VS Code are downloaded during install.

- [ ] **Step 6: Delete obsolete VS Code manifest**

```bash
git rm packaging/windows/vscode-manifest.json
```

- [ ] **Step 7: Run packaging tests**

Run:

```bash
go test ./internal/vscode -run 'TestWindowsPackaging|TestWindowsInstallScriptsIncludeVSCodeInstaller|TestWindowsEnsureCodexScriptCallsAgentctlInstallCodex|TestWindowsInnoInstallerFrontendInstallUsesEstimatedProgress|TestWindowsInnoInstallerStopsRunningAppProcessesBeforeReplacingFiles' -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add packaging/windows/installer.iss packaging/windows/install.ps1 scripts/package-windows.sh scripts/package-windows-zip.sh internal/vscode/install_test.go
git add -u packaging/windows/vscode-manifest.json
git commit -m "feat: build light windows installer payload"
```

---

### Task 8: Full Verification and Manual Windows Notes

**Files:**
- Modify if needed: `docs/superpowers/notes/2026-06-12-light-windows-installer-verification.md`

- [ ] **Step 1: Run full Go tests**

Run:

```bash
go test ./...
```

Expected: PASS for all Go packages.

- [ ] **Step 2: Run packaging script dry checks**

Run:

```bash
bash -n scripts/package-windows.sh
bash -n scripts/package-windows-zip.sh
```

Expected: both commands exit 0.

- [ ] **Step 3: Search for removed bundled payload references**

Run:

```bash
rg -n "codex-x86_64-pc-windows-msvc.exe|VSCodeUserSetup|vscode-installer.exe|VSCODE_CACHE|CODEX_CACHE|vscode-manifest.json" packaging scripts internal cmd
```

Expected: no matches except historical docs under `docs/` if the command is intentionally expanded to include docs.

- [ ] **Step 4: Build package preflight if artifacts exist**

If `dist/windows/*.exe`, `internal/ui/assets/dist/index.html`, and `extensions/agentserver-app/agentserver-app-0.1.0.vsix` exist, run:

```bash
bash scripts/package-windows-zip.sh
```

Expected: zip is built without requiring `codex.exe` or `vscode-installer.exe`. If base build artifacts are missing, record the missing files printed by the script and do not claim the package build passed.

- [ ] **Step 5: Write manual verification note**

If a Windows test machine is not available in this session, create `docs/superpowers/notes/2026-06-12-light-windows-installer-verification.md`:

```markdown
# Light Windows Installer Verification Notes

Automated verification completed:

- `go test ./...`
- `bash -n scripts/package-windows.sh`
- `bash -n scripts/package-windows-zip.sh`
- removed-payload reference search

Manual Windows checks still required:

1. Build and run the Inno installer on a clean Windows user.
2. Confirm setup file table excludes `codex.exe` and `vscode-installer.exe`.
3. Confirm install downloads Codex runtime into `%LOCALAPPDATA%\agentserver-app\bin\codex.exe`.
4. Confirm default Codex Desktop mode completes onboarding.
5. Confirm minimal VS Code mode downloads and runs the Store bootstrapper.
6. Confirm VS Code settings point at `%LOCALAPPDATA%\agentserver-app\bin\codex.exe`.
```

- [ ] **Step 6: Commit verification notes if created**

```bash
git add docs/superpowers/notes/2026-06-12-light-windows-installer-verification.md
git commit -m "docs: record light installer verification notes"
```

Skip this commit if Windows manual verification was completed and no note file is needed.

---

## Self-Review

**Spec coverage:**

- No bundled `codex.exe`: Task 7 removes Inno/zip/script payload references and adds negative tests.
- No bundled VS Code installer exe: Task 7 removes VS Code installer payload references and deletes `vscode-manifest.json`.
- Download Codex from domestic npm mirrors: Tasks 1-4 add manifest, resolver, integrity verification, extraction, and `agentctl install-codex`.
- Pinned-first, latest fallback: Tasks 2-3 test pinned candidates and latest metadata fallback.
- Extract full runtime tree: Tasks 2-3 require `bin`, `codex-path`, and `codex-resources`.
- Minimal VS Code downloads Store bootstrapper: Tasks 5-6 implement Go and PowerShell bootstrapper download.
- Onboarding configuration remains existing owner: Task 3 keeps `ConfigureVSCode`; Task 5 changes only install source.
- Packaging scripts and tests updated: Tasks 6-7.
- Manual Windows verification: Task 8.

**Placeholder scan:** This plan has no prohibited placeholder markers or vague open-ended implementation steps.

**Type consistency:** The plan consistently uses `codexruntime.Manifest`, `codexruntime.Options`, `codexruntime.InstallResult`, `vscode.InstallPlan.BootstrapperURL`, `vscode.InstallPlan.StoreProductID`, `ui.Deps.CodexManifestPath`, and `ui.Deps.CodexRuntimeEnsure`.
