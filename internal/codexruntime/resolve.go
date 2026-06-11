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
	return PackageCandidate{
		URL:       payload.Dist.Tarball,
		Integrity: payload.Dist.Integrity,
		Shasum:    payload.Dist.Shasum,
	}, nil
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
