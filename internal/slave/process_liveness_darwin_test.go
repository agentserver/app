//go:build darwin

package slave

import "testing"

// 强核验：pid 1 在 macOS 上是 launchd，不是 slave-agent，因此带上 expectedExe 时
// 应判为 processMismatch，而非历史弱行为 processMatch。
func TestInspectOSProcessOnDarwinVerifiesExeNotBlindly(t *testing.T) {
	got, err := inspectOSProcess(1, "/Applications/星池指挥官.app/Contents/MacOS/slave-agent")
	if err != nil {
		t.Fatalf("inspectOSProcess returned error on darwin: %v", err)
	}
	if got != processMismatch {
		t.Fatalf("inspection=%v, want processMismatch (launchd != slave-agent)", got)
	}
}
