//go:build windows

package folderpicker

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	clsctxInprocServer = 0x1

	coinitApartmentThreaded = 0x2

	swRestore = 9

	fosPickFolders     = 0x00000020 // FOS_PICKFOLDERS
	fosForceFilesystem = 0x00000040 // FOS_FORCEFILESYSTEM
	fosPathMustExist   = 0x00000800 // FOS_PATHMUSTEXIST

	sigdnFileSysPath = 0x80058000

	comdlgECancelled = 0x800704C7 // COMDLG_E_CANCELLED
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")

	user32                  = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")

	// CLSID_FileOpenDialog
	clsidFileOpenDialog = windows.GUID{
		Data1: 0xDC1C5A9C,
		Data2: 0xE88A,
		Data3: 0x4DDE,
		Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7},
	}
	// IID_IFileOpenDialog
	iidIFileOpenDialog = windows.GUID{
		Data1: 0xD57C7288,
		Data2: 0xD4AD,
		Data3: 0x4768,
		Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60},
	}
)

type iFileOpenDialog struct {
	lpVtbl *iFileOpenDialogVtbl
}

type iFileOpenDialogVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	Show           uintptr

	SetFileTypes     uintptr
	SetFileTypeIndex uintptr
	GetFileTypeIndex uintptr
	Advise           uintptr
	Unadvise         uintptr
	SetOptions       uintptr
	GetOptions       uintptr
	SetDefaultFolder uintptr
	SetFolder        uintptr
	GetFolder        uintptr
	GetCurrentSelect uintptr
	SetFileName      uintptr
	GetFileName      uintptr
	SetTitle         uintptr
	SetOkButtonLabel uintptr
	SetFileNameLabel uintptr
	GetResult        uintptr
	AddPlace         uintptr
	SetDefaultExt    uintptr
	Close            uintptr
	SetClientGuid    uintptr
	ClearClientData  uintptr
	SetFilter        uintptr

	GetResults       uintptr
	GetSelectedItems uintptr
}

type iShellItem struct {
	lpVtbl *iShellItemVtbl
}

type iShellItemVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	BindToHandler  uintptr
	GetParent      uintptr
	GetDisplayName uintptr
	GetAttributes  uintptr
	Compare        uintptr
}

func selectFolder(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if failed(hr) {
		return "", hresultError("CoInitializeEx", hr)
	}
	defer procCoUninitialize.Call()

	var dialog *iFileOpenDialog
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&dialog)),
	)
	if failed(hr) {
		return "", hresultError("CoCreateInstance(FileOpenDialog)", hr)
	}
	defer dialog.release()

	if title, err := windows.UTF16PtrFromString("选择允许被远程控制的文件夹"); err == nil {
		if hr := dialog.setTitle(title); failed(hr) {
			return "", hresultError("IFileDialog.SetTitle", hr)
		}
	}

	var options uintptr
	if hr := dialog.getOptions(&options); failed(hr) {
		return "", hresultError("IFileDialog.GetOptions", hr)
	}
	options |= fosPickFolders | fosForceFilesystem | fosPathMustExist
	if hr := dialog.setOptions(options); failed(hr) {
		return "", hresultError("IFileDialog.SetOptions", hr)
	}

	hr = dialog.show(foregroundWindowOwner())
	if uint32(hr) == comdlgECancelled {
		return "", nil
	}
	if failed(hr) {
		return "", hresultError("IFileDialog.Show", hr)
	}

	var item *iShellItem
	if hr := dialog.getResult(&item); failed(hr) {
		return "", hresultError("IFileDialog.GetResult", hr)
	}
	if item == nil {
		return "", nil
	}
	defer item.release()

	var pathPtr *uint16
	if hr := item.getDisplayName(sigdnFileSysPath, &pathPtr); failed(hr) {
		return "", hresultError("IShellItem.GetDisplayName", hr)
	}
	if pathPtr == nil {
		return "", nil
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pathPtr)))

	return windows.UTF16PtrToString(pathPtr), nil
}

func foregroundWindowOwner() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	_, _, _ = procShowWindow.Call(hwnd, swRestore)
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	return hwnd
}

func (d *iFileOpenDialog) release() {
	_, _, _ = syscall.SyscallN(d.lpVtbl.Release, uintptr(unsafe.Pointer(d)))
}

func (d *iFileOpenDialog) show(owner uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(d.lpVtbl.Show, uintptr(unsafe.Pointer(d)), owner)
	return hr
}

func (d *iFileOpenDialog) setTitle(title *uint16) uintptr {
	hr, _, _ := syscall.SyscallN(d.lpVtbl.SetTitle, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(title)))
	return hr
}

func (d *iFileOpenDialog) getOptions(options *uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(d.lpVtbl.GetOptions, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(options)))
	return hr
}

func (d *iFileOpenDialog) setOptions(options uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(d.lpVtbl.SetOptions, uintptr(unsafe.Pointer(d)), options)
	return hr
}

func (d *iFileOpenDialog) getResult(item **iShellItem) uintptr {
	hr, _, _ := syscall.SyscallN(d.lpVtbl.GetResult, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(item)))
	return hr
}

func (i *iShellItem) release() {
	_, _, _ = syscall.SyscallN(i.lpVtbl.Release, uintptr(unsafe.Pointer(i)))
}

func (i *iShellItem) getDisplayName(sigDN uintptr, path **uint16) uintptr {
	hr, _, _ := syscall.SyscallN(i.lpVtbl.GetDisplayName, uintptr(unsafe.Pointer(i)), sigDN, uintptr(unsafe.Pointer(path)))
	return hr
}

func failed(hr uintptr) bool {
	return uint32(hr)&0x80000000 != 0
}

func hresultError(op string, hr uintptr) error {
	return fmt.Errorf("%s failed: HRESULT 0x%08X", op, uint32(hr))
}
