package slave

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestMachineStoreInitializesComputerNameOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")
	store := NewMachineStore(path)

	first, err := store.Ensure("61414-PC")
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}
	if first.ComputerName != "61414-PC" {
		t.Fatalf("ComputerName=%q", first.ComputerName)
	}
	if first.MachineID == "" {
		t.Fatal("MachineID empty")
	}

	second, err := store.Ensure("OTHER-PC")
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}
	if second.ComputerName != "61414-PC" {
		t.Fatalf("ComputerName changed to %q", second.ComputerName)
	}
	if second.MachineID != first.MachineID {
		t.Fatalf("MachineID changed from %q to %q", first.MachineID, second.MachineID)
	}
}

func TestMachineStoreRejectsBlankComputerName(t *testing.T) {
	store := NewMachineStore(filepath.Join(t.TempDir(), "machine.json"))
	if _, err := store.Ensure("   "); err == nil {
		t.Fatal("expected blank computer name error")
	}
}

func TestMachineStoreLoadMissingFileReturnsNotExist(t *testing.T) {
	store := NewMachineStore(filepath.Join(t.TempDir(), "machine.json"))
	if _, err := store.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load error=%v, want os.ErrNotExist", err)
	}
}

func TestMachineStoreLoadRejectsIncompleteIdentities(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing machine id", body: `{"computer_name":"INSTALL-PC"}`},
		{name: "missing computer name", body: `{"machine_id":"machine-123"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "machine.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewMachineStore(path).Load(); err == nil {
				t.Fatal("expected incomplete identity error")
			}
		})
	}
}

func TestMachineStoreEnsureBlankNameReturnsExistingIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")
	store := NewMachineStore(path)

	first, err := store.Ensure("61414-PC")
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}
	second, err := store.Ensure("   ")
	if err != nil {
		t.Fatalf("Ensure blank after existing: %v", err)
	}
	if second != first {
		t.Fatalf("machine changed from %+v to %+v", first, second)
	}
}

func TestMachineStoreEnsureCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "machine", "machine.json")
	if _, err := NewMachineStore(path).Ensure("61414-PC"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat machine file: %v", err)
	}
}

func TestMachineStoreConcurrentFirstCreateKeepsOneIdentity(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(0)
	if prevProcs < 8 {
		runtime.GOMAXPROCS(8)
		t.Cleanup(func() {
			runtime.GOMAXPROCS(prevProcs)
		})
	}

	const attempts = 50
	const workers = 64

	for attempt := 0; attempt < attempts; attempt++ {
		path := filepath.Join(t.TempDir(), "machine.json")
		store := NewMachineStore(path)
		start := make(chan struct{})
		results := make([]Machine, workers)
		errs := make([]error, workers)

		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			i := i
			go func() {
				defer wg.Done()
				<-start
				results[i], errs[i] = store.Ensure(fmt.Sprintf("PC-%02d-%02d", attempt, i))
			}()
		}

		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("attempt %d worker %d Ensure: %v", attempt, i, err)
			}
		}

		final, err := store.Load()
		if err != nil {
			t.Fatalf("attempt %d Load final: %v", attempt, err)
		}
		for i, got := range results {
			if got.MachineID != final.MachineID || got.ComputerName != final.ComputerName {
				t.Fatalf("attempt %d worker %d got %+v, final %+v", attempt, i, got, final)
			}
		}
	}
}

func TestMachineStoreCreatesMachineFile0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not preserve POSIX file mode bits")
	}

	path := filepath.Join(t.TempDir(), "machine.json")
	if _, err := NewMachineStore(path).Ensure("61414-PC"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%#o, want 0600", got)
	}
}

func TestMachineStoreSuccessfulPublishCleansTempFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewMachineStore(filepath.Join(dir, "machine.json")).Ensure("61414-PC"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	assertNoMachineTempFiles(t, dir)
}

func TestMachineStoreLoadsInstallerWrittenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine.json")
	body, err := json.Marshal(Machine{
		MachineID:    "machine-123",
		ComputerName: "INSTALL-PC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := NewMachineStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.MachineID != "machine-123" || got.ComputerName != "INSTALL-PC" {
		t.Fatalf("machine=%+v", got)
	}
}

func assertNoMachineTempFiles(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".machine-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary machine file left behind: %s", entry.Name())
		}
	}
}
