//go:build darwin

package slave

import "testing"

// Strong verification: pid 1 on macOS is launchd, not slave-agent, so with an
// expectedExe set the inspection must be processMismatch — not the historical
// weak behavior of processMatch (trusting any live PID).
func TestInspectOSProcessOnDarwinVerifiesExeNotBlindly(t *testing.T) {
	got, err := inspectOSProcess(1, "/Applications/星池指挥官.app/Contents/MacOS/slave-agent")
	if err != nil {
		t.Fatalf("inspectOSProcess returned error on darwin: %v", err)
	}
	if got != processMismatch {
		t.Fatalf("inspection=%v, want processMismatch (launchd != slave-agent)", got)
	}
}
