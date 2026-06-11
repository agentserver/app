package main

// Smoke check: each test-* command's runner is wired and links against the
// internal packages it needs. The actual side-effecting behavior is exercised
// only on Windows (via remote scripted invocations); on Linux these compile
// and are smoke-tested by unit tests for the underlying internal/* packages.

import "testing"

func TestRunnersCompile(t *testing.T) {
	// Just reference each func so a missing import fails the build.
	_ = runTestInstallVSCode
	_ = runTestDownloadCodex
	_ = runInstallCodex
	_ = runTestConfigure
	_ = runTestOpenFolder
	_ = runTestMarkComplete
}
