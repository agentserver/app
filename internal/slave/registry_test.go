package slave

import (
	"encoding/json"
	"errors"
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

	if _, err := reg.Create(m, CreateInput{Folder: filepath.Join(dir, "missing")}); !errors.Is(err, ErrInvalidCreateInput) {
		t.Fatal("expected missing folder error")
	}
	if _, err := reg.Create(m, CreateInput{Folder: "   "}); !errors.Is(err, ErrInvalidCreateInput) {
		t.Fatal("expected blank folder error")
	}

	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create(m, CreateInput{Folder: folder, Name: "123456789012345678901"}); !errors.Is(err, ErrInvalidCreateInput) {
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
	if _, err := reg.Create(m, CreateInput{Folder: folderB, Name: "worker"}); !errors.Is(err, ErrSlaveConflict) {
		t.Fatal("expected duplicate display name error")
	}
}

func TestRegistryFindByFolderUsesCanonicalPath(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "repo-link")
	if err := os.Symlink(folder, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "host"}
	created, err := reg.Create(m, CreateInput{Folder: folder, Name: "repo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok, err := reg.FindByFolder(link)
	if err != nil {
		t.Fatalf("FindByFolder: %v", err)
	}
	if !ok {
		t.Fatal("FindByFolder did not find existing slave")
	}
	if got.ID != created.ID {
		t.Fatalf("got ID=%q want %q", got.ID, created.ID)
	}
}

func TestRegistryFindByFolderSkipsStaleStoredFolderAndFindsLaterMatch(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "missing")
	registryPath := filepath.Join(dir, "slaves.json")
	all := []Slave{
		{
			ID:          "stale",
			Name:        "stale",
			DisplayName: "host-stale",
			Folder:      stale,
			Status:      StatusStopped,
		},
		{
			ID:          "valid",
			Name:        "valid",
			DisplayName: "host-valid",
			Folder:      folder,
			Status:      StatusStopped,
		},
	}
	b, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(registryPath, filepath.Join(dir, "slaves"))

	got, ok, err := reg.FindByFolder(folder)
	if err != nil {
		t.Fatalf("FindByFolder: %v", err)
	}
	if !ok {
		t.Fatal("FindByFolder did not find existing slave")
	}
	if got.ID != "valid" {
		t.Fatalf("got ID=%q want valid", got.ID)
	}
}

func TestRegistryEnsureForFolderReusesExistingSlave(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "host"}

	first, created, err := reg.EnsureForFolder(m, CreateInput{Folder: folder, Name: "repo"})
	if err != nil {
		t.Fatalf("EnsureForFolder first: %v", err)
	}
	if !created {
		t.Fatal("first EnsureForFolder should create")
	}
	second, created, err := reg.EnsureForFolder(m, CreateInput{Folder: folder, Name: "different"})
	if err != nil {
		t.Fatalf("EnsureForFolder second: %v", err)
	}
	if created {
		t.Fatal("second EnsureForFolder should reuse")
	}
	if second.ID != first.ID || second.Name != "repo" {
		t.Fatalf("second=%+v first=%+v", second, first)
	}
}

func TestRegistryCreateStoresCanonicalFolderWhenInputIsSymlink(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "repo-link")
	if err := os.Symlink(folder, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "host"}

	got, err := reg.Create(m, CreateInput{Folder: link, Name: "repo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want, err := CanonicalFolder(folder)
	if err != nil {
		t.Fatalf("CanonicalFolder: %v", err)
	}
	if got.Folder != want {
		t.Fatalf("Folder=%q want %q", got.Folder, want)
	}
}

func TestRegistryCreateDefaultNameUsesSymlinkInputBasename(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "repo-link")
	if err := os.Symlink(folder, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "host"}

	got, err := reg.Create(m, CreateInput{Folder: link})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "repo-link" {
		t.Fatalf("Name=%q want repo-link", got.Name)
	}
	if got.DisplayName != "host-repo-link" {
		t.Fatalf("DisplayName=%q want host-repo-link", got.DisplayName)
	}
	want, err := CanonicalFolder(folder)
	if err != nil {
		t.Fatalf("CanonicalFolder: %v", err)
	}
	if got.Folder != want {
		t.Fatalf("Folder=%q want %q", got.Folder, want)
	}
}

func TestRegistryEnsureForFolderDefaultNameUsesSymlinkInputBasenameAndReusesCanonicalTarget(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "repo-link")
	if err := os.Symlink(folder, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "host"}

	first, created, err := reg.EnsureForFolder(m, CreateInput{Folder: link})
	if err != nil {
		t.Fatalf("EnsureForFolder first: %v", err)
	}
	if !created {
		t.Fatal("first EnsureForFolder should create")
	}
	if first.Name != "repo-link" {
		t.Fatalf("first Name=%q want repo-link", first.Name)
	}
	want, err := CanonicalFolder(folder)
	if err != nil {
		t.Fatalf("CanonicalFolder: %v", err)
	}
	if first.Folder != want {
		t.Fatalf("first Folder=%q want %q", first.Folder, want)
	}

	second, created, err := reg.EnsureForFolder(m, CreateInput{Folder: folder})
	if err != nil {
		t.Fatalf("EnsureForFolder second: %v", err)
	}
	if created {
		t.Fatal("second EnsureForFolder should reuse")
	}
	if second.ID != first.ID {
		t.Fatalf("second ID=%q want %q", second.ID, first.ID)
	}
	if second.Name != "repo-link" {
		t.Fatalf("second Name=%q want repo-link", second.Name)
	}
}

func TestCanonicalFolderReturnsInvalidCreateInputWhenEvalSymlinksFails(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("resolve failed")
	prev := evalSymlinks
	evalSymlinks = func(string) (string, error) {
		return "", wantErr
	}
	t.Cleanup(func() {
		evalSymlinks = prev
	})

	_, err := CanonicalFolder(folder)
	if !errors.Is(err, ErrInvalidCreateInput) {
		t.Fatalf("error=%v, want ErrInvalidCreateInput", err)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v, want wrapped eval error", err)
	}
}

func TestRegistryEnsureForFolderRejectsDuplicateDisplayNameForDifferentFolders(t *testing.T) {
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
	m := Machine{MachineID: "machine-1", ComputerName: "host"}

	if _, created, err := reg.EnsureForFolder(m, CreateInput{Folder: folderA, Name: "worker"}); err != nil || !created {
		t.Fatalf("EnsureForFolder first created=%v err=%v", created, err)
	}
	if _, _, err := reg.EnsureForFolder(m, CreateInput{Folder: folderB, Name: "worker"}); !errors.Is(err, ErrSlaveConflict) {
		t.Fatalf("expected duplicate display name error, got %v", err)
	}
}

func TestRegistryConcurrentEnsureForFolderCreatesOneSlave(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(0)
	if prevProcs < 8 {
		runtime.GOMAXPROCS(8)
		t.Cleanup(func() {
			runtime.GOMAXPROCS(prevProcs)
		})
	}

	dir := t.TempDir()
	folder := filepath.Join(dir, "repo")
	if err := mkdir(folder); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(filepath.Join(dir, "slaves.json"), filepath.Join(dir, "slaves"))
	m := Machine{MachineID: "machine-1", ComputerName: "host"}
	const workers = 64
	start := make(chan struct{})
	results := make([]Slave, workers)
	created := make([]bool, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			results[i], created[i], errs[i] = reg.EnsureForFolder(m, CreateInput{Folder: folder})
		}()
	}

	close(start)
	wg.Wait()

	var id string
	createCount := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d EnsureForFolder: %v", i, err)
		}
		if results[i].ID == "" {
			t.Fatalf("worker %d empty ID: %+v", i, results[i])
		}
		if id == "" {
			id = results[i].ID
		}
		if results[i].ID != id {
			t.Fatalf("worker %d ID=%q want %q", i, results[i].ID, id)
		}
		if created[i] {
			createCount++
		}
	}
	if createCount != 1 {
		t.Fatalf("created count=%d want 1", createCount)
	}
	all, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("registry has %d slaves, want 1: %+v", len(all), all)
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
