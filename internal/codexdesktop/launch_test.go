package codexdesktop

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestThreadURLWithoutFolder(t *testing.T) {
	if got := ThreadURL(""); got != "codex://threads/new" {
		t.Fatalf("ThreadURL empty = %q", got)
	}
}

func TestThreadURLWithFolder(t *testing.T) {
	got := ThreadURL(`C:\Users\Test User\Project`)
	if !strings.HasPrefix(got, "codex://threads/new?path=") {
		t.Fatalf("url=%q", got)
	}
	if !strings.Contains(got, "Test+User") {
		t.Fatalf("folder path not encoded: %q", got)
	}
}

func TestThreadURLRoundTripWindowsPath(t *testing.T) {
	folder := `C:\Users\Test User\Project`
	parsed, err := url.Parse(ThreadURL(folder))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := parsed.Query().Get("path"); got != folder {
		t.Fatalf("path=%q want %q", got, folder)
	}
}

func readyLaunchDetection() Detected {
	return Detected{
		Installed:         true,
		Status:            StatusReady,
		PackageFamilyName: ChatGPTPackageFamily,
		InstallLocation:   `C:\Program Files\WindowsApps\ChatGPT`,
		AppUserModelID:    ChatGPTPackageFamily + "!ChatGPT",
		SchemeRegistered:  true,
		SchemeTargetValid: true,
	}
}

func TestLaunchWithOptionsUsesActivatorAndConfirmsNewProcess(t *testing.T) {
	var activated string
	snapshots := []ProcessSnapshot{{}, {42: {}}, {42: {}}}
	err := launchWithOptions(context.Background(), `C:\Project`, launchOptions{
		detect: func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(_ context.Context, det Detected, rawURL string) error {
			if det.AppUserModelID != readyLaunchDetection().AppUserModelID {
				t.Fatalf("activation detection=%+v", det)
			}
			activated = rawURL
			return nil
		},
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) {
			got := snapshots[0]
			snapshots = snapshots[1:]
			return got, nil
		},
		pollInterval: time.Nanosecond,
		sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("launchWithOptions: %v", err)
	}
	if !strings.HasPrefix(activated, "codex://threads/new?path=") {
		t.Fatalf("activated=%q", activated)
	}
}

func TestLaunchWithOptionsAcceptsStableAlreadyRunningProcess(t *testing.T) {
	snapshots := []ProcessSnapshot{{7: {}}, {7: {}}, {7: {}}}
	snapshotCalls := 0
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect:   func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(context.Context, Detected, string) error { return nil },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) {
			snapshotCalls++
			got := snapshots[0]
			snapshots = snapshots[1:]
			return got, nil
		},
		pollInterval: time.Nanosecond,
		sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("launchWithOptions: %v", err)
	}
	if snapshotCalls != 3 {
		t.Fatalf("snapshot calls=%d, want baseline plus two consecutive post-launch samples", snapshotCalls)
	}
}

func TestLaunchWithOptionsRejectsUnavailableStatesBeforeOpening(t *testing.T) {
	for _, tc := range []struct {
		name string
		det  Detected
		err  error
	}{
		{name: "not installed", det: Detected{Status: StatusNotInstalled}, err: ErrNotFound},
		{name: "scheme missing", det: Detected{Installed: true, Status: StatusSchemeMissing}, err: ErrSchemeMissing},
		{name: "scheme invalid", det: Detected{Installed: true, Status: StatusSchemeTargetInvalid}, err: ErrSchemeTargetInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			activated := false
			err := launchWithOptions(context.Background(), "", launchOptions{
				detect: func() (Detected, error) { return tc.det, tc.err },
				activate: func(context.Context, Detected, string) error {
					activated = true
					return nil
				},
				snapshot: func(context.Context, Detected) (ProcessSnapshot, error) { return nil, nil },
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v, want %v", err, tc.err)
			}
			if activated {
				t.Fatal("activator called after failed preflight")
			}
		})
	}
}

func TestLaunchWithOptionsSurfacesOperationalDetectionFailure(t *testing.T) {
	want := errors.New("PowerShell unavailable")
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect:   func() (Detected, error) { return Detected{}, want },
		activate: func(context.Context, Detected, string) error { return nil },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) { return nil, nil },
	})
	if !errors.Is(err, want) || errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want operational error", err)
	}
}

func TestLaunchWithOptionsWrapsActivationFailureWithGuidance(t *testing.T) {
	want := errors.New("ActivateForProtocol failed")
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect:   func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(context.Context, Detected, string) error { return want },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) { return ProcessSnapshot{}, nil },
	})
	if !errors.Is(err, want) || !errors.Is(err, ErrLaunchFailed) {
		t.Fatalf("err=%v, want activation cause and ErrLaunchFailed", err)
	}
	for _, text := range []string{ShortDisplayName, "Repair", "Reset", "Reinstall"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("err=%v, missing %q", err, text)
		}
	}
}

func TestLaunchWithOptionsBoundsNonCooperativeActivatorByTotalDeadline(t *testing.T) {
	activationStarted := make(chan struct{})
	releaseActivator := make(chan struct{})
	done := make(chan error, 1)
	var snapshotCalls atomic.Int32

	release := func() {
		select {
		case <-releaseActivator:
		default:
			close(releaseActivator)
		}
	}
	defer release()

	go func() {
		done <- launchWithOptions(context.Background(), "", launchOptions{
			detect: func() (Detected, error) { return readyLaunchDetection(), nil },
			activate: func(context.Context, Detected, string) error {
				close(activationStarted)
				<-releaseActivator
				return nil
			},
			snapshot: func(context.Context, Detected) (ProcessSnapshot, error) {
				snapshotCalls.Add(1)
				return ProcessSnapshot{}, nil
			},
			timeout:      20 * time.Millisecond,
			pollInterval: time.Millisecond,
			sleep:        sleepWithContext,
		})
	}()

	select {
	case <-activationStarted:
	case err := <-done:
		t.Fatalf("launchWithOptions returned before activator blocked: %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("activator did not start")
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrLaunchFailed) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v, want ErrLaunchFailed wrapping context deadline", err)
		}
		if got := snapshotCalls.Load(); got != 1 {
			t.Fatalf("snapshot calls=%d, want only baseline snapshot before blocked activation", got)
		}
	case <-time.After(500 * time.Millisecond):
		release()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("launchWithOptions remained blocked even after releasing test activator")
		}
		t.Fatal("launchWithOptions did not return promptly when activator ignored total deadline")
	}
}

func TestLaunchWithOptionsDoesNotExposeSensitiveActivationCause(t *testing.T) {
	want := errors.New(`ActivateForProtocol failed for C:\Users\alice\secret.txt HKEY_CLASSES_ROOT\codex token=top-secret`)
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect:   func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(context.Context, Detected, string) error { return want },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) { return ProcessSnapshot{}, nil },
	})
	if !errors.Is(err, want) || !errors.Is(err, ErrLaunchFailed) {
		t.Fatalf("err=%v, want activation cause and ErrLaunchFailed", err)
	}
	for _, forbidden := range []string{"alice", "secret.txt", "HKEY_CLASSES_ROOT", "top-secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("user-visible launch error leaked %q: %q", forbidden, err)
		}
	}
	for _, wantText := range []string{ShortDisplayName, "Repair", "Reset", "Reinstall"} {
		if !strings.Contains(err.Error(), wantText) {
			t.Fatalf("err=%v, missing %q", err, wantText)
		}
	}
}

func TestLaunchWithOptionsDoesNotExposeOperationalDetectionDetails(t *testing.T) {
	want := errors.New(`PowerShell failed: C:\Users\alice\secret.ps1 token=top-secret`)
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect:   func() (Detected, error) { return Detected{}, want },
		activate: func(context.Context, Detected, string) error { return nil },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) { return nil, nil },
	})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want detector cause", err)
	}
	for _, forbidden := range []string{"alice", "secret.ps1", "top-secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("user-visible preflight error leaked %q: %q", forbidden, err)
		}
	}
	if !strings.Contains(err.Error(), "启动前无法检查") {
		t.Fatalf("err=%v, want safe preflight diagnosis", err)
	}
}

func TestLaunchWithOptionsRejectsActivationWithoutTrustedProcess(t *testing.T) {
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect:   func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(context.Context, Detected, string) error { return nil },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) { return ProcessSnapshot{}, nil },
		sleep:    func(context.Context, time.Duration) error { return context.DeadlineExceeded },
	})
	if !errors.Is(err, ErrLaunchFailed) {
		t.Fatalf("err=%v, want ErrLaunchFailed", err)
	}
	if !strings.Contains(err.Error(), "无法确认") {
		t.Fatalf("err=%v, want confirmation diagnosis", err)
	}
}

func TestLaunchWithOptionsSurfacesSnapshotFailure(t *testing.T) {
	want := errors.New("CIM denied")
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect:   func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(context.Context, Detected, string) error { return nil },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) {
			return nil, want
		},
	})
	if !errors.Is(err, want) || !errors.Is(err, ErrLaunchFailed) {
		t.Fatalf("err=%v, want snapshot cause and ErrLaunchFailed", err)
	}
}

func TestLaunchWithOptionsHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := launchWithOptions(ctx, `C:\Project`, launchOptions{
		detect: func() (Detected, error) {
			called = true
			return readyLaunchDetection(), nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if called {
		t.Fatal("detector called for canceled context")
	}
}

func TestLaunchWithOptionsRejectsInvalidSuccessfulDetectionBeforeSecurityActions(t *testing.T) {
	det := readyLaunchDetection()
	det.AppUserModelID = LegacyCodexPackageFamily + "!Codex"
	actionCalled := false
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect: func() (Detected, error) { return det, nil },
		activate: func(context.Context, Detected, string) error {
			actionCalled = true
			return nil
		},
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) {
			actionCalled = true
			return nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "AppUserModelID") {
		t.Fatalf("err=%v, want AppUserModelID validation failure", err)
	}
	if actionCalled {
		t.Fatal("snapshot or activation called for inconsistent successful detection")
	}
}

func TestLaunchWithOptionsRequiresSecurityDependencies(t *testing.T) {
	base := launchOptions{
		detect:   func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(context.Context, Detected, string) error { return nil },
		snapshot: func(context.Context, Detected) (ProcessSnapshot, error) { return nil, nil },
	}
	for _, tc := range []struct {
		name   string
		mutate func(*launchOptions)
	}{
		{name: "detector", mutate: func(opts *launchOptions) { opts.detect = nil }},
		{name: "activator", mutate: func(opts *launchOptions) { opts.activate = nil }},
		{name: "snapshotter", mutate: func(opts *launchOptions) { opts.snapshot = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			tc.mutate(&opts)
			err := launchWithOptions(context.Background(), "", opts)
			if !errors.Is(err, ErrLaunchFailed) || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("err=%v, want ErrLaunchFailed naming missing %s", err, tc.name)
			}
		})
	}
}

func TestLaunchWithOptionsUsesOneTotalDeadlineBeforeFirstSnapshot(t *testing.T) {
	type contextUse struct {
		name string
		ctx  context.Context
	}
	var uses []contextUse
	record := func(name string, ctx context.Context) {
		uses = append(uses, contextUse{name: name, ctx: ctx})
	}
	snapshots := []ProcessSnapshot{{}, {42: {}}, {42: {}}}
	err := launchWithOptions(context.Background(), "", launchOptions{
		detect: func() (Detected, error) { return readyLaunchDetection(), nil },
		activate: func(ctx context.Context, _ Detected, _ string) error {
			record("activate", ctx)
			return nil
		},
		snapshot: func(ctx context.Context, _ Detected) (ProcessSnapshot, error) {
			record("snapshot", ctx)
			got := snapshots[0]
			snapshots = snapshots[1:]
			return got, nil
		},
		timeout:      time.Second,
		pollInterval: time.Nanosecond,
		sleep: func(ctx context.Context, _ time.Duration) error {
			record("sleep", ctx)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("launchWithOptions: %v", err)
	}
	if len(uses) < 3 || uses[0].name != "snapshot" {
		t.Fatalf("context uses=%v, want initial snapshot first", uses)
	}
	totalCtx := uses[0].ctx
	if _, ok := totalCtx.Deadline(); !ok {
		t.Fatal("initial snapshot context has no total deadline")
	}
	for _, use := range uses[1:] {
		if use.ctx != totalCtx {
			t.Fatalf("%s received a different context; total deadline was reset", use.name)
		}
	}
}

func TestWindowsLaunchDirectlyActivatesDetectedAUMID(t *testing.T) {
	body, err := os.ReadFile("launch_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"activateForProtocol",
		"applicationActivationManagerVtbl",
		"ActivateForProtocol",
		"CoCreateInstance",
		"SHCreateItemFromParsingName",
		"SHCreateShellItemArrayFromShellItem",
		"det.AppUserModelID",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("launch_windows.go missing direct activation contract %q:\n%s", want, source)
		}
	}
	for _, notWant := range []string{"browser.Open", "browser.OpenContext", "ShellExecute", "rundll32"} {
		if strings.Contains(source, notWant) {
			t.Fatalf("launch_windows.go contains forbidden Shell activation %q:\n%s", notWant, source)
		}
	}
	for _, want := range []string{"procCoInitializeEx.Call", "hresultFailed(comInitHRESULT)"} {
		if !strings.Contains(source, want) {
			t.Fatalf("launch_windows.go must classify CoInitializeEx HRESULTs directly; missing %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "windows.CoInitializeEx(") {
		t.Fatalf("x/sys treats successful S_FALSE as an error; classify the raw HRESULT instead:\n%s", source)
	}
}

func TestHRESULTFailureClassificationAcceptsSFalse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		hresult    uintptr
		wantFailed bool
	}{
		{name: "S_OK", hresult: 0, wantFailed: false},
		{name: "S_FALSE", hresult: 1, wantFailed: false},
		{name: "RPC_E_CHANGED_MODE", hresult: 0x80010106, wantFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hresultFailed(tc.hresult); got != tc.wantFailed {
				t.Fatalf("hresultFailed(0x%08X)=%t, want %t", uint32(tc.hresult), got, tc.wantFailed)
			}
		})
	}
}

func TestLaunchInjectionAPIIsPrivate(t *testing.T) {
	body, err := os.ReadFile("launch.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, exported := range []string{"type LaunchOptions ", "func LaunchWithOptions("} {
		if strings.Contains(source, exported) {
			t.Fatalf("launch.go exposes security injection API %q:\n%s", exported, source)
		}
	}
}

func TestParseProcessSnapshotFiltersPackageUserAndSession(t *testing.T) {
	payload := `{
      "current_user_sid":"S-1-5-21-current",
      "current_session_id":3,
      "processes":[
        {"pid":10,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"c:/program files/windowsapps/chatgpt/","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":11,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"S-1-5-21-other","session_id":3},
        {"pid":12,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"S-1-5-21-current","session_id":4},
        {"pid":13,"package_family_name":"OpenAI.Codex_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":14,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\Other","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":15,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":16,"package_family_name":"openai.chatgpt-desktop_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":17,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"s-1-5-21-current","session_id":3}
      ]
    }`
	got, err := parseProcessSnapshot([]byte(payload), ChatGPTPackageFamily, `C:\Program Files\WindowsApps\ChatGPT`)
	if err != nil {
		t.Fatalf("parseProcessSnapshot: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got=%v, want only current user/session/package PID", got)
	}
	if _, ok := got[10]; !ok {
		t.Fatalf("got=%v, want PID 10", got)
	}
}

func TestParseProcessSnapshotRejectsEmptyExpectedInstallLocation(t *testing.T) {
	payload := `{"current_user_sid":"S-1-5-21-current","current_session_id":3,"processes":[]}`
	if _, err := parseProcessSnapshot([]byte(payload), ChatGPTPackageFamily, ""); err == nil {
		t.Fatal("expected empty install location to fail closed")
	}
}

func TestParseProcessSnapshotRequiresCanonicalFinalInstallLocationMatch(t *testing.T) {
	payload := `{
      "current_user_sid":"S-1-5-21-current",
      "current_session_id":3,
      "processes":[
        {"pid":20,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"\\\\?\\C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":21,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"\\\\?\\C:\\Program Files\\WindowsApps\\ChatGPT.Evil","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":22,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"\\\\?\\C:\\Outside\\ChatGPT","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":23,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"final-path-error","owner_sid":"S-1-5-21-current","session_id":3},
        {"pid":24,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"\\\\?\\C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"S-1-5-21-other","session_id":3},
        {"pid":25,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"\\\\?\\C:\\Program Files\\WindowsApps\\ChatGPT","owner_sid":"S-1-5-21-current","session_id":4}
      ]
    }`
	got, err := parseProcessSnapshot([]byte(payload), ChatGPTPackageFamily, `C:\Program Files\WindowsApps\ChatGPT`)
	if err != nil {
		t.Fatalf("parseProcessSnapshot: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got=%v, want only final path matching detector location and current SID/session", got)
	}
	if _, ok := got[20]; !ok {
		t.Fatalf("got=%v, want PID 20", got)
	}
}

func TestProcessSnapshotPowerShellUsesHandleFinalPathsAndFailsClosed(t *testing.T) {
	body, err := os.ReadFile("process_snapshot_windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		ChatGPTPackageFamily,
		LegacyCodexPackageFamily,
		"Add-Type -TypeDefinition @'",
		"CreateFileW",
		"GetFinalPathNameByHandleW",
		"FILE_READ_ATTRIBUTES",
		"FILE_SHARE_READ",
		"FILE_SHARE_WRITE",
		"FILE_SHARE_DELETE",
		"FILE_FLAG_BACKUP_SEMANTICS",
		"Resolve-ChatGPTCodexFinalPath",
		"Resolve-ChatGPTCodexFinalDirectoryPath",
		"InstallLocation",
		"ExecutablePath",
		"SessionId",
		"WindowsIdentity",
		"GetOwnerSid",
		"RootWithSeparator",
		"FinalInstallLocation",
		"[System.StringComparison]::OrdinalIgnoreCase",
		"install_location",
		"[string]$matchedPackage.FinalInstallLocation",
		"Resolve-ChatGPTCodexFinalDirectoryPath -Path $location",
		"Resolve-ChatGPTCodexFinalPath -Path $executablePath",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("process snapshot script missing %q:\n%s", want, source)
		}
	}
	for _, notWant := range []string{
		"[System.IO.Path]::GetFullPath",
		"ProcessName",
		"Name -like",
		"CommandLine -like",
		"*ChatGPT*",
		"*Codex*",
	} {
		if strings.Contains(source, notWant) {
			t.Fatalf("process snapshot script contains unsafe matching %q:\n%s", notWant, source)
		}
	}
}

func TestWindowsCodexPowerShellUsesAbsoluteSystemExecutable(t *testing.T) {
	for _, file := range []string{"detect_windows.go", "launch_windows.go"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		for _, want := range []string{"systemExecutablePath", `"powershell.exe"`} {
			if !strings.Contains(source, want) {
				t.Fatalf("%s missing %q:\n%s", file, want, source)
			}
		}
		for _, forbidden := range []string{
			`exec.Command("powershell.exe"`,
			`exec.CommandContext(ctx, "powershell.exe"`,
		} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s launches PowerShell through PATH via %q:\n%s", file, forbidden, source)
			}
		}
	}

	body, err := os.ReadFile("windows_system.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"windows.GetSystemDirectory",
		"filepath.IsAbs",
		"filepath.Join",
		`"WindowsPowerShell"`,
		`"v1.0"`,
		`"powershell.exe"`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("windows_system.go missing absolute System32 resolution %q:\n%s", want, source)
		}
	}
}
