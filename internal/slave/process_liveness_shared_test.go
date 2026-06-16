//go:build !windows

package slave

import "testing"

func TestInspectDecisionLogic(t *testing.T) {
	// Use pid 1 (always live and killable as root on Linux CI / dev hosts) so the
	// pre-resolve syscall.Kill probe does not short-circuit to processMissing.
	// The resolve function is injected, so the actual exe of pid 1 is irrelevant;
	// only the string it returns is compared against expectedExe.
	//
	// Paths are chosen to be host-independent:
	//   - matching: identical strings hit the filepath.Abs equality fast path in
	//     sameExecutable (no filesystem stat required).
	//   - mismatch: two real, distinct files (/proc/self/exe vs /dev/null) that
	//     exist and stat successfully on every Linux host so sameExecutable returns
	//     (false, nil) rather than an os.ErrNotExist mapped to processMissing.
	matching := func(pid int) (string, error) { return "/opt/app/slave-agent", nil }
	mismatch := func(pid int) (string, error) { return "/dev/null", nil }

	got, err := inspectOSProcessWith(1, "/opt/app/slave-agent", matching)
	if err != nil || got != processMatch {
		t.Errorf("matching exe: got=%v err=%v want processMatch", got, err)
	}
	got, err = inspectOSProcessWith(1, "/proc/self/exe", mismatch)
	if err != nil || got != processMismatch {
		t.Errorf("mismatched exe: got=%v err=%v want processMismatch", got, err)
	}
	got, err = inspectOSProcessWith(1, "", mismatch)
	if err != nil || got != processMatch {
		t.Errorf("empty expected: got=%v err=%v want processMatch", got, err)
	}
	got, _ = inspectOSProcessWith(0, "/opt/app/slave-agent", matching)
	if got != processMissing {
		t.Errorf("pid<=0: got=%v want processMissing", got)
	}
}
