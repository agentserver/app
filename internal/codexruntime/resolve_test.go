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
