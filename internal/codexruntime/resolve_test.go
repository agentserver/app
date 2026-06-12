package codexruntime

import "testing"

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

func TestPinnedCandidatesIncludeRepoPinnedFallbackVersions(t *testing.T) {
	m := Manifest{
		PinnedVersion: "0.136.0-win32-x64",
		Pinned: PinnedPackage{
			Integrity: "sha512-pinned",
			Shasum:    "abc123",
			URLs:      []string{"https://mirror1/codex-0.136.0.tgz", "https://mirror2/codex-0.136.0.tgz"},
		},
		FallbackPinned: []PinnedPackage{
			{
				Version:   "0.139.0-win32-x64",
				Integrity: "sha512-fallback",
				Shasum:    "def456",
				URLs:      []string{"https://mirror1/codex-0.139.0.tgz", "https://mirror2/codex-0.139.0.tgz"},
			},
		},
	}
	got := PinnedCandidates(m)
	if len(got) != 4 {
		t.Fatalf("candidates=%#v", got)
	}
	if got[0].Version != "0.136.0-win32-x64" || got[0].URL != "https://mirror1/codex-0.136.0.tgz" || got[0].Source != "pinned" {
		t.Fatalf("first candidate=%#v", got[0])
	}
	if got[1].Version != "0.136.0-win32-x64" || got[1].URL != "https://mirror2/codex-0.136.0.tgz" || got[1].Source != "pinned" {
		t.Fatalf("second candidate=%#v", got[1])
	}
	if got[2].Version != "0.139.0-win32-x64" || got[2].URL != "https://mirror1/codex-0.139.0.tgz" || got[2].Source != "fallback-pinned" {
		t.Fatalf("third candidate=%#v", got[2])
	}
	if got[3].Version != "0.139.0-win32-x64" || got[3].URL != "https://mirror2/codex-0.139.0.tgz" || got[3].Source != "fallback-pinned" {
		t.Fatalf("fourth candidate=%#v", got[3])
	}
}
