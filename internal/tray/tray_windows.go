//go:build windows

package tray

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	trayClassName = "AgentserverPkgTrayWindow"

	wmClose   = 0x0010
	wmCommand = 0x0111
	wmDestroy = 0x0002
	wmNull    = 0x0000
	wmApp     = 0x8000
	wmTray    = wmApp + 1

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	notifyIconVersion4 = 4
	niifInfo           = 0x00000001

	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040
	idiApplication = 32512

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfGrayed    = 0x00000001

	tpmRightButton = 0x00000002

	idOpenDashboard    = 1001
	idOpenFrontend     = 1002
	idOpenSubscription = 1003
	idQuit             = 1004

	errorClassAlreadyExists = syscall.Errno(1410)
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procLoadIconW        = user32.NewProc("LoadIconW")
	procDestroyIcon      = user32.NewProc("DestroyIcon")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenuW      = user32.NewProc("AppendMenuW")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSetForegroundWnd = user32.NewProc("SetForegroundWindow")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	windowProcCallback = windows.NewCallback(windowProc)
	classOnce          sync.Once
	classErr           error

	appsMu sync.Mutex
	apps   = map[windows.Handle]*windowsApp{}
)

type windowsApp struct {
	iconPath string

	mu           sync.Mutex
	hwnd         windows.Handle
	hIcon        windows.Handle
	ownsIcon     bool
	state        State
	actions      Actions
	ready        chan struct{}
	readyOnce    sync.Once
	shutdown     chan struct{}
	shutdownOnce sync.Once
}

func New(iconPath string) App {
	return &windowsApp{
		iconPath: iconPath,
		state: State{
			Tooltip:           "星池指挥官\n额度暂不可用",
			FiveHour:          "5小时额度：暂不可用",
			SevenDay:          "7天额度：暂不可用",
			OpenFrontendLabel: "启动 Codex Desktop",
		},
		ready:    make(chan struct{}),
		shutdown: make(chan struct{}),
	}
}

func (a *windowsApp) Run(ctx context.Context, actions Actions) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	a.mu.Lock()
	a.actions = actions
	a.mu.Unlock()

	hwnd, icon, ownsIcon, err := createTrayWindow(a.iconPath)
	if err != nil {
		a.closeReady()
		a.closeShutdown()
		return err
	}
	a.mu.Lock()
	a.hwnd = hwnd
	a.hIcon = icon
	a.ownsIcon = ownsIcon
	a.mu.Unlock()

	registerApp(hwnd, a)
	defer unregisterApp(hwnd)
	defer a.closeShutdown()
	defer a.destroyIcon()
	defer a.destroyWindow()

	if err := a.addIcon(); err != nil {
		a.closeReady()
		return err
	}
	a.closeReady()
	defer a.deleteIcon()

	go func() {
		select {
		case <-ctx.Done():
			a.postClose()
		case <-a.shutdown:
		}
	}()

	err = messageLoop()
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (a *windowsApp) Update(st State) {
	a.mu.Lock()
	a.state = st
	hwnd := a.hwnd
	a.mu.Unlock()
	if hwnd == 0 {
		return
	}
	if err := a.modifyIcon(); err != nil {
		// There is no error channel on the App interface; keep Update best-effort.
		_ = err
	}
}

func (a *windowsApp) Notify(title, message string) error {
	if err := a.waitReady(); err != nil {
		return err
	}
	a.mu.Lock()
	nid := a.notifyDataLocked(nifInfo)
	a.mu.Unlock()
	copyUTF16(nid.szInfoTitle[:], title)
	copyUTF16(nid.szInfo[:], message)
	nid.dwInfoFlags = niifInfo
	return shellNotify(nimModify, &nid)
}

func createTrayWindow(iconPath string) (windows.Handle, windows.Handle, bool, error) {
	hInstance, err := getModuleHandle()
	if err != nil {
		return 0, 0, false, err
	}
	if err := registerTrayClass(hInstance); err != nil {
		return 0, 0, false, err
	}
	className, _ := windows.UTF16PtrFromString(trayClassName)
	title, _ := windows.UTF16PtrFromString("Agentserver Tray")
	hwnd, _, e1 := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		uintptr(hInstance),
		0,
	)
	if hwnd == 0 {
		return 0, 0, false, fmt.Errorf("CreateWindowExW: %w", errno(e1))
	}
	icon, ownsIcon := loadTrayIcon(iconPath)
	return windows.Handle(hwnd), icon, ownsIcon, nil
}

func registerTrayClass(hInstance windows.Handle) error {
	classOnce.Do(func() {
		className, _ := windows.UTF16PtrFromString(trayClassName)
		wc := wndClassEx{
			cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
			lpfnWndProc:   windowProcCallback,
			hInstance:     hInstance,
			lpszClassName: className,
		}
		r1, _, e1 := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if r1 == 0 && e1 != errorClassAlreadyExists {
			classErr = fmt.Errorf("RegisterClassExW: %w", errno(e1))
		}
	})
	return classErr
}

func getModuleHandle() (windows.Handle, error) {
	h, _, e1 := procGetModuleHandleW.Call(0)
	if h == 0 {
		return 0, fmt.Errorf("GetModuleHandleW: %w", errno(e1))
	}
	return windows.Handle(h), nil
}

func loadTrayIcon(iconPath string) (windows.Handle, bool) {
	if iconPath != "" {
		if _, err := os.Stat(iconPath); err == nil {
			path, err := windows.UTF16PtrFromString(iconPath)
			if err == nil {
				h, _, _ := procLoadImageW.Call(
					0,
					uintptr(unsafe.Pointer(path)),
					imageIcon,
					0,
					0,
					lrLoadFromFile|lrDefaultSize,
				)
				if h != 0 {
					return windows.Handle(h), true
				}
			}
		}
	}
	h, _, _ := procLoadIconW.Call(0, idiApplication)
	return windows.Handle(h), false
}

func (a *windowsApp) addIcon() error {
	a.mu.Lock()
	nid := a.notifyDataLocked(addIconNotifyFlags())
	a.mu.Unlock()
	if err := shellNotify(nimAdd, &nid); err != nil {
		return err
	}
	nid.uVersionOrTimeout = notifyIconVersion4
	_ = shellNotify(nimSetVersion, &nid)
	return nil
}

func (a *windowsApp) modifyIcon() error {
	a.mu.Lock()
	nid := a.notifyDataLocked(tooltipNotifyFlags())
	a.mu.Unlock()
	return shellNotify(nimModify, &nid)
}

func (a *windowsApp) deleteIcon() {
	a.mu.Lock()
	nid := a.notifyDataLocked(0)
	a.mu.Unlock()
	_ = shellNotify(nimDelete, &nid)
}

func (a *windowsApp) notifyDataLocked(flags uint32) notifyIconData {
	nid := notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             a.hwnd,
		uID:              trayIconID,
		uFlags:           flags,
		uCallbackMessage: wmTray,
		hIcon:            a.hIcon,
	}
	copyUTF16(nid.szTip[:], a.state.Tooltip)
	return nid
}

func (a *windowsApp) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	state := a.currentState()
	appendMenu(menu, mfString, idOpenDashboard, "打开控制台")
	appendMenu(menu, mfString, idOpenFrontend, openFrontendMenuLabel(state))
	appendMenu(menu, mfString, idOpenSubscription, "打开订阅页")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString|mfGrayed, 0, state.FiveHour)
	appendMenu(menu, mfString|mfGrayed, 0, state.SevenDay)
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idQuit, "退出星池指挥官")

	var pt point
	if r1, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r1 == 0 {
		return
	}
	a.mu.Lock()
	hwnd := a.hwnd
	a.mu.Unlock()
	procSetForegroundWnd.Call(uintptr(hwnd))
	procTrackPopupMenu.Call(menu, tpmRightButton, uintptr(pt.x), uintptr(pt.y), 0, uintptr(hwnd), 0)
	procPostMessageW.Call(uintptr(hwnd), wmNull, 0, 0)
}

func (a *windowsApp) handleCommand(id uint16) {
	actions := a.currentActions()
	switch id {
	case idOpenDashboard:
		runAction(actions.OpenDashboard)
	case idOpenFrontend:
		runAction(actions.OpenFrontend)
	case idOpenSubscription:
		runAction(actions.OpenSubscription)
	case idQuit:
		if actions.Quit != nil {
			runAction(actions.Quit)
		} else {
			a.postClose()
		}
	}
}

func (a *windowsApp) currentState() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *windowsApp) currentActions() Actions {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.actions
}

func (a *windowsApp) waitReady() error {
	select {
	case <-a.ready:
		return nil
	case <-a.shutdown:
		return fmt.Errorf("tray stopped")
	case <-time.After(2 * time.Second):
		return fmt.Errorf("tray not ready")
	}
}

func (a *windowsApp) postClose() {
	a.mu.Lock()
	hwnd := a.hwnd
	a.mu.Unlock()
	if hwnd == 0 {
		return
	}
	procPostMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
}

func (a *windowsApp) destroyIcon() {
	a.mu.Lock()
	hIcon := a.hIcon
	ownsIcon := a.ownsIcon
	a.hIcon = 0
	a.mu.Unlock()
	if hIcon != 0 && ownsIcon {
		procDestroyIcon.Call(uintptr(hIcon))
	}
}

func (a *windowsApp) destroyWindow() {
	a.mu.Lock()
	hwnd := a.hwnd
	a.hwnd = 0
	a.mu.Unlock()
	if hwnd != 0 {
		procDestroyWindow.Call(uintptr(hwnd))
	}
}

func (a *windowsApp) closeReady() {
	a.readyOnce.Do(func() { close(a.ready) })
}

func (a *windowsApp) closeShutdown() {
	a.shutdownOnce.Do(func() { close(a.shutdown) })
}

func registerApp(hwnd windows.Handle, app *windowsApp) {
	appsMu.Lock()
	defer appsMu.Unlock()
	apps[hwnd] = app
}

func unregisterApp(hwnd windows.Handle) {
	appsMu.Lock()
	defer appsMu.Unlock()
	delete(apps, hwnd)
}

func appForWindow(hwnd uintptr) *windowsApp {
	appsMu.Lock()
	defer appsMu.Unlock()
	return apps[windows.Handle(hwnd)]
}

func windowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	app := appForWindow(hwnd)
	switch msg {
	case wmTray:
		if app != nil {
			event, ok := trayCallbackEvent(lParam)
			if !ok {
				return 0
			}
			switch event {
			case wmRButtonUp, wmContextMenu:
				app.showMenu()
				return 0
			case wmLButtonDbl:
				runAction(app.currentActions().OpenDashboard)
				return 0
			}
		}
	case wmCommand:
		if app != nil {
			app.handleCommand(uint16(wParam & 0xffff))
			return 0
		}
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r1, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r1
}

func messageLoop() error {
	var m msg
	for {
		r1, _, e1 := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(r1) {
		case -1:
			return fmt.Errorf("GetMessageW: %w", errno(e1))
		case 0:
			return nil
		default:
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

func appendMenu(menu uintptr, flags uint32, id uintptr, text string) {
	if flags&mfSeparator != 0 {
		procAppendMenuW.Call(menu, uintptr(flags), 0, 0)
		return
	}
	ptr, _ := windows.UTF16PtrFromString(text)
	procAppendMenuW.Call(menu, uintptr(flags), id, uintptr(unsafe.Pointer(ptr)))
}

func shellNotify(message uint32, nid *notifyIconData) error {
	r1, _, e1 := procShellNotifyIconW.Call(uintptr(message), uintptr(unsafe.Pointer(nid)))
	if r1 == 0 {
		return fmt.Errorf("Shell_NotifyIconW: %w", errno(e1))
	}
	return nil
}

func runAction(fn func()) {
	if fn == nil {
		return
	}
	go fn()
}

func copyUTF16(dst []uint16, s string) {
	if len(dst) == 0 {
		return
	}
	encoded := windows.StringToUTF16(s)
	if len(encoded) > len(dst) {
		encoded = encoded[:len(dst)]
		encoded[len(encoded)-1] = 0
	}
	copy(dst, encoded)
}

func errno(err error) error {
	if err == nil {
		return syscall.EINVAL
	}
	if err == syscall.Errno(0) {
		return syscall.EINVAL
	}
	return err
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type notifyIconData struct {
	cbSize            uint32
	hWnd              windows.Handle
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             windows.Handle
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uVersionOrTimeout uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          windows.GUID
	hBalloonIcon      windows.Handle
}

type point struct {
	x int32
	y int32
}

type msg struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}
