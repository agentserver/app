package slave

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestRegistryCreatesSlaveWithDefaultFolderName(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "project-a")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	got, err := reg.Create(m, CreateInput{Folder: folder})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "project-a" {
		t.Fatalf("Name=%q", got.Name)
	}
	if got.DisplayName != "61414-PC-project-a" {
		t.Fatalf("DisplayName=%q", got.DisplayName)
	}
	if got.Folder != folder {
		t.Fatalf("Folder=%q", got.Folder)
	}
	if got.ConfigPath == "" || filepath.Dir(got.ConfigPath) != filepath.Join(dir, "slaves", got.ID) {
		t.Fatalf("ConfigPath=%q", got.ConfigPath)
	}
}

func TestRegistryCreatesSlaveWithCustomImmutableName(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	got, err := reg.Create(m, CreateInput{Folder: folder, Name: "前端调试"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "前端调试" || got.DisplayName != "61414-PC-前端调试" {
		t.Fatalf("slave=%+v", got)
	}

	loaded, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 || loaded[0].DisplayName != got.DisplayName {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestRegistryRejectsInvalidCreateInput(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	if _, err := reg.Create(m, CreateInput{Folder: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("expected missing folder error")
	}
	if _, err := reg.Create(m, CreateInput{Folder: "   "}); err == nil {
		t.Fatal("expected blank folder error")
	}

	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create(m, CreateInput{Folder: folder, Name: "123456789012345678901"}); err == nil {
		t.Fatal("expected long name error")
	}
	if _, err := reg.Create(Machine{}, CreateInput{Folder: folder}); err == nil {
		t.Fatal("expected missing machine error")
	}
}

func TestRegistryRejectsDuplicateDisplayName(t *testing.T) {
	dir := t.TempDir()
	folderA := filepath.Join(dir, "a")
	folderB := filepath.Join(dir, "b")
	if err := mkdir(folderA); err != nil {
		t.Fatal(err)
	}
	if err := mkdir(folderB); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	if _, err := reg.Create(m, CreateInput{Folder: folderA, Name: "worker"}); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := reg.Create(m, CreateInput{Folder: folderB, Name: "worker"}); err == nil {
		t.Fatal("expected duplicate display name error")
	}
}

func TestRegistryConcurrentCreatesPreserveAllSlaves(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(0)
	if prevProcs < 8 {
		runtime.GOMAXPROCS(8)
		t.Cleanup(func() {
			runtime.GOMAXPROCS(prevProcs)
		})
	}

	const attempts = 20
	const workers = 64

	for attempt := 0; attempt < attempts; attempt++ {
		dir := t.TempDir()
		folder := filepath.Join(dir, "repo")
		if err := mkdir(folder); err != nil {
			t.Fatal(err)
		}
		reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
		m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}
		start := make(chan struct{})
		errs := make([]error, workers)

		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			i := i
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = reg.Create(m, CreateInput{
					Folder: folder,
					Name:   fmt.Sprintf("worker-%02d", i),
				})
			}()
		}

		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("attempt %d worker %d Create: %v", attempt, i, err)
			}
		}
		got, err := reg.List()
		if err != nil {
			t.Fatalf("attempt %d List: %v", attempt, err)
		}
		if len(got) != workers {
			t.Fatalf("attempt %d saved %d slaves, want %d: %+v", attempt, len(got), workers, got)
		}

		ids := make(map[string]bool, workers)
		displayNames := make(map[string]bool, workers)
		for _, sl := range got {
			if sl.Status != StatusStopped {
				t.Fatalf("attempt %d slave %s status=%q", attempt, sl.DisplayName, sl.Status)
			}
			if sl.ID == "" {
				t.Fatalf("attempt %d empty slave ID: %+v", attempt, sl)
			}
			if ids[sl.ID] {
				t.Fatalf("attempt %d duplicate slave ID %q", attempt, sl.ID)
			}
			ids[sl.ID] = true
			if displayNames[sl.DisplayName] {
				t.Fatalf("attempt %d duplicate display name %q", attempt, sl.DisplayName)
			}
			displayNames[sl.DisplayName] = true
		}
		for i := 0; i < workers; i++ {
			want := fmt.Sprintf("61414-PC-worker-%02d", i)
			if !displayNames[want] {
				t.Fatalf("attempt %d missing display name %q in %+v", attempt, want, got)
			}
		}
	}
}

func TestRegistrySaveNarrowsExistingRegistryFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not preserve POSIX file mode bits")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "slaves.json")
	if err := os.WriteFile(path, []byte("[]\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(path, filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	if _, err := reg.Create(m, CreateInput{Folder: folder}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("registry file mode=%#o, want 0600", got)
	}
}

func TestRegistryRejectsInvalidPathCharactersInName(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	m := Machine{MachineID: "machine-1", ComputerName: "61414-PC"}

	for _, name := range []string{
		`bad\name`,
		"bad/name",
		"bad:name",
		"bad*name",
		"bad?name",
		`bad"name`,
		"bad<name",
		"bad>name",
		"bad|name",
	} {
		t.Run(name, func(t *testing.T) {
			reg := NewRegistry(filepath.Join(dir, name+".json"), filepath.Join(dir, "slaves", name))
			if _, err := reg.Create(m, CreateInput{Folder: folder, Name: name}); err == nil {
				t.Fatal("expected invalid path character error")
			}
		})
	}
}

func mkdir(path string) error {
	return os.MkdirAll(path, 0o755)
}
