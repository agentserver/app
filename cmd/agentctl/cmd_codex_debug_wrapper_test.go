package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexDebugExtractsThreadPathFromResumeError(t *testing.T) {
	stderr := `Error: thread/resume: thread/resume failed: failed to read thread: thread-store internal error: failed to read thread C:\Users\DELL\.codex\sessions\2026\07\06\rollout-2026-07-06T18-35-46-019f36ff-9026-7bb1-8a2d-a18220aafe0b.jsonl: rollout at C:\Users\DELL\.codex\sessions\2026\07\06\rollout-2026-07-06T18-35-46-019f36ff-9026-7bb1-8a2d-a18220aafe0b.jsonl does not start with session metadata`

	got := codexDebugThreadPaths(stderr)

	want := `C:\Users\DELL\.codex\sessions\2026\07\06\rollout-2026-07-06T18-35-46-019f36ff-9026-7bb1-8a2d-a18220aafe0b.jsonl`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("paths=%v, want [%s]", got, want)
	}
}

func TestCodexDebugSummarizesSessionMetadataWithoutPromptContent(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "rollout-2026-07-06T18-35-46-019f36ff-9026-7bb1-8a2d-a18220aafe0b.jsonl")
	writeFileForTest(t, sessionPath, `{"timestamp":"2026-07-06T10:35:46.397Z","type":"session_meta","payload":{"session_id":"019f36ff-9026-7bb1-8a2d-a18220aafe0b","id":"019f36ff-9026-7bb1-8a2d-a18220aafe0b","cli_version":"0.142.5","cwd":"C:\\Users\\DELL\\Documents\\secret-project"}}`+"\n"+
		`{"timestamp":"2026-07-06T10:36:00.000Z","type":"event_msg","payload":{"message":"do not leak this prompt"}}`+"\n")

	got := codexDebugSessionSummary(sessionPath)

	for _, want := range []string{
		"exists=true",
		"size=",
		"sha256=",
		"first_type=session_meta",
		"payload_session_id=019f36ff-9026-7bb1-8a2d-a18220aafe0b",
		"payload_cli_version=0.142.5",
		"payload_cwd=C:\\Users\\DELL\\Documents\\secret-project",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "do not leak this prompt") {
		t.Fatalf("summary leaked prompt content:\n%s", got)
	}
}

func writeFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
