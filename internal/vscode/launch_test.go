package vscode

import "testing"

func TestLaunchArgsIncludeLocaleAndDirs(t *testing.T) {
	args := LaunchArgs(`C:\data`, `C:\ext`, `C:\work`)
	want := []string{
		"--locale", "zh-cn",
		"--user-data-dir", `C:\data`,
		"--extensions-dir", `C:\ext`,
		"--disable-updates",
		"--disable-extension", "GitHub.copilot",
		"--disable-extension", "GitHub.copilot-chat",
		`C:\work`,
	}
	if len(args) != len(want) {
		t.Fatalf("args=%v want=%v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args=%v want=%v", args, want)
		}
	}
}

func TestUpsertEnvReplacesExistingKey(t *testing.T) {
	got := UpsertEnv([]string{"A=1", "OPENAI_API_KEY=old", "B=2"}, "OPENAI_API_KEY", "new")
	want := []string{"A=1", "OPENAI_API_KEY=new", "B=2"}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}
