//go:build windows

package codexdesktop

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/agentserver/agentserver-pkg/internal/process"
	"golang.org/x/sys/windows"
)

//go:embed process_snapshot_windows.ps1
var processSnapshotWindowsPowerShell string

var (
	ole32                                   = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx                      = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance                    = ole32.NewProc("CoCreateInstance")
	shell32                                 = windows.NewLazySystemDLL("shell32.dll")
	procSHCreateItemFromParsingName         = shell32.NewProc("SHCreateItemFromParsingName")
	procSHCreateShellItemArrayFromShellItem = shell32.NewProc("SHCreateShellItemArrayFromShellItem")

	clsidApplicationActivationManager = windows.GUID{
		Data1: 0x45ba127d,
		Data2: 0x10a8,
		Data3: 0x46ea,
		Data4: [8]byte{0x8a, 0xb7, 0x56, 0xea, 0x90, 0x78, 0x94, 0x3c},
	}
	iidApplicationActivationManager = windows.GUID{
		Data1: 0x2e941141,
		Data2: 0x7f97,
		Data3: 0x4756,
		Data4: [8]byte{0xba, 0x1d, 0x9d, 0xec, 0xde, 0x89, 0x4a, 0x3d},
	}
	iidShellItem = windows.GUID{
		Data1: 0x43826d1e,
		Data2: 0xe718,
		Data3: 0x42ee,
		Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe},
	}
	iidShellItemArray = windows.GUID{
		Data1: 0xb63ea76d,
		Data2: 0x1f85,
		Data3: 0x456f,
		Data4: [8]byte{0xa1, 0x9c, 0x48, 0x15, 0x9e, 0xfa, 0x85, 0x8b},
	}
)

func Launch(ctx context.Context, folder string) error {
	return launchWithOptions(ctx, folder, defaultLaunchOptions())
}

type comObject struct {
	lpVtbl *comObjectVtbl
}

type comObjectVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type applicationActivationManager struct {
	lpVtbl *applicationActivationManagerVtbl
}

type applicationActivationManagerVtbl struct {
	QueryInterface      uintptr
	AddRef              uintptr
	Release             uintptr
	ActivateApplication uintptr
	ActivateForFile     uintptr
	ActivateForProtocol uintptr
}

func defaultLaunchOptions() launchOptions {
	return launchOptions{
		detect:   Detect,
		activate: activateForProtocol,
		snapshot: snapshotTrustedPackageProcesses,
	}
}

func activateForProtocol(ctx context.Context, det Detected, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDetected(det); err != nil {
		return fmt.Errorf("validate direct activation target: %w", err)
	}

	aumid, err := windows.UTF16PtrFromString(det.AppUserModelID)
	if err != nil {
		return fmt.Errorf("encode AppUserModelID: %w", err)
	}
	protocolURL, err := windows.UTF16PtrFromString(rawURL)
	if err != nil {
		return fmt.Errorf("encode codex protocol URL: %w", err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	comInitHRESULT, _, _ := procCoInitializeEx.Call(
		0,
		uintptr(windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE),
	)
	if hresultFailed(comInitHRESULT) {
		return hresultError("CoInitializeEx", comInitHRESULT)
	}
	defer windows.CoUninitialize()

	var item *comObject
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(protocolURL)),
		0,
		uintptr(unsafe.Pointer(&iidShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if hresultFailed(hr) {
		return hresultError("SHCreateItemFromParsingName", hr)
	}
	if item == nil {
		return fmt.Errorf("SHCreateItemFromParsingName returned a nil IShellItem")
	}
	defer item.release()

	var itemArray *comObject
	hr, _, _ = procSHCreateShellItemArrayFromShellItem.Call(
		uintptr(unsafe.Pointer(item)),
		uintptr(unsafe.Pointer(&iidShellItemArray)),
		uintptr(unsafe.Pointer(&itemArray)),
	)
	if hresultFailed(hr) {
		return hresultError("SHCreateShellItemArrayFromShellItem", hr)
	}
	if itemArray == nil {
		return fmt.Errorf("SHCreateShellItemArrayFromShellItem returned a nil IShellItemArray")
	}
	defer itemArray.release()

	if err := ctx.Err(); err != nil {
		return err
	}
	var manager *applicationActivationManager
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidApplicationActivationManager)),
		0,
		windows.CLSCTX_LOCAL_SERVER,
		uintptr(unsafe.Pointer(&iidApplicationActivationManager)),
		uintptr(unsafe.Pointer(&manager)),
	)
	if hresultFailed(hr) {
		return hresultError("CoCreateInstance(ApplicationActivationManager)", hr)
	}
	if manager == nil {
		return fmt.Errorf("CoCreateInstance(ApplicationActivationManager) returned nil")
	}
	defer manager.release()

	var processID uint32
	hr, _, _ = syscall.SyscallN(
		manager.lpVtbl.ActivateForProtocol,
		uintptr(unsafe.Pointer(manager)),
		uintptr(unsafe.Pointer(aumid)),
		uintptr(unsafe.Pointer(itemArray)),
		uintptr(unsafe.Pointer(&processID)),
	)
	runtime.KeepAlive(aumid)
	runtime.KeepAlive(protocolURL)
	if hresultFailed(hr) {
		if protocolActivationNeedsAppActivationFallback(hr) {
			return activateApplication(ctx, manager, det, rawURL)
		}
		return hresultError("IApplicationActivationManager.ActivateForProtocol", hr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func activateApplication(ctx context.Context, manager *applicationActivationManager, det Detected, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDetected(det); err != nil {
		return fmt.Errorf("validate direct application activation target: %w", err)
	}
	if manager == nil || manager.lpVtbl == nil {
		return fmt.Errorf("IApplicationActivationManager is nil")
	}

	aumid, err := windows.UTF16PtrFromString(det.AppUserModelID)
	if err != nil {
		return fmt.Errorf("encode AppUserModelID: %w", err)
	}
	arguments, err := windows.UTF16PtrFromString(rawURL)
	if err != nil {
		return fmt.Errorf("encode codex protocol URL: %w", err)
	}

	var processID uint32
	hr, _, _ := syscall.SyscallN(
		manager.lpVtbl.ActivateApplication,
		uintptr(unsafe.Pointer(manager)),
		uintptr(unsafe.Pointer(aumid)),
		uintptr(unsafe.Pointer(arguments)),
		0,
		uintptr(unsafe.Pointer(&processID)),
	)
	runtime.KeepAlive(aumid)
	runtime.KeepAlive(arguments)
	if hresultFailed(hr) {
		return hresultError("IApplicationActivationManager.ActivateApplication", hr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (object *comObject) release() {
	if object != nil && object.lpVtbl != nil {
		_, _, _ = syscall.SyscallN(object.lpVtbl.Release, uintptr(unsafe.Pointer(object)))
	}
}

func (manager *applicationActivationManager) release() {
	if manager != nil && manager.lpVtbl != nil {
		_, _, _ = syscall.SyscallN(manager.lpVtbl.Release, uintptr(unsafe.Pointer(manager)))
	}
}

func hresultError(operation string, hr uintptr) error {
	return fmt.Errorf("%s failed: HRESULT 0x%08X", operation, uint32(hr))
}

func snapshotTrustedPackageProcesses(ctx context.Context, det Detected) (ProcessSnapshot, error) {
	if err := validateDetected(det); err != nil {
		return nil, fmt.Errorf("cannot snapshot processes for invalid detection: %w", err)
	}
	script := "$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue';\n" +
		processSnapshotWindowsPowerShell + "\nGet-ChatGPTCodexProcessSnapshot | ConvertTo-Json -Depth 4 -Compress"
	powerShell, err := systemExecutablePath("powershell.exe")
	if err != nil {
		return nil, fmt.Errorf("resolve system PowerShell: %w", err)
	}
	cmd := exec.CommandContext(ctx, powerShell, "-NoProfile", "-NonInteractive", "-Command", script)
	process.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("snapshot ChatGPT/Codex package processes: %w; output: %s", err, strings.TrimSpace(string(out)))
	}
	return parseProcessSnapshot(out, det.PackageFamilyName, det.InstallLocation)
}
