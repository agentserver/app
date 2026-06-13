//go:build darwin

package slave

import "testing"

func TestInspectOSProcessOnDarwinTrustsLivePIDWhenExpectedExeProvided(t *testing.T) {
	got, err := inspectOSProcess(1, "/Applications/Agent/slave-agent")
	if err != nil {
		t.Fatalf("inspectOSProcess returned error on darwin: %v", err)
	}
	if got != processMatch {
		t.Fatalf("inspection=%v, want processMatch", got)
	}
}
