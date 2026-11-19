//go:build windows

package tray

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type MenuItem struct {
	Label     string
	Disabled  bool
	Default   bool
	Separator bool
	Action    func()
}

type Config struct {
	Tag       string
	Tooltip   string
	IconPNG   []byte
	OnShow    func()
	BuildMenu func() []MenuItem
}

type Tray struct {
	cfg        Config
	hwnd       syscall.Handle
	hicon      syscall.Handle
	taskbarMsg uint32
	showMsg    uint32
	done       chan struct{}

	mu      sync.Mutex
	tooltip string
	closed  bool
}

const (
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmNull        = 0x0000
	wmUser        = 0x0400
	wmTrayIcon    = wmUser + 1
	wmUpdateTip   = wmUser + 2
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfDisabled  = 0x00000002
	mfSeparator = 0x00000800

	tpmRightButton  = 0x0002
	tpmReturnCmd    = 0x0100
	tpmNonotify     = 0x0080
	tpmBottomAlign  = 0x0020
	tpmLeftAlign    = 0x0000

	smCxSmIcon = 49
	smCySmIcon = 50
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procGetModuleHandleW        = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW        = user32.NewProc("RegisterClassExW")
	procUnregisterClassW        = user32.NewProc("UnregisterClassW")
	procCreateWindowExW         = user32.NewProc("CreateWindowExW")
	procDestroyWindow           = user32.NewProc("DestroyWindow")
	procDefWindowProcW          = user32.NewProc("DefWindowProcW")
	procGetMessageW             = user32.NewProc("GetMessageW")
	procTranslateMessage        = user32.NewProc("TranslateMessage")
	procDispatchMessageW        = user32.NewProc("DispatchMessageW")
	procPostMessageW            = user32.NewProc("PostMessageW")
	procPostQuitMessage         = user32.NewProc("PostQuitMessage")
	procRegisterWindowMessageW  = user32.NewProc("RegisterWindowMessageW")
	procCreateIconFromResEx     = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon             = user32.NewProc("DestroyIcon")
	procGetSystemMetrics        = user32.NewProc("GetSystemMetrics")
	procCreatePopupMenu         = user32.NewProc("CreatePopupMenu")
	procDestroyMenu             = user32.NewProc("DestroyMenu")
	procAppendMenuW             = user32.NewProc("AppendMenuW")
	procSetMenuDefaultItem      = user32.NewProc("SetMenuDefaultItem")
	procTrackPopupMenuEx        = user32.NewProc("TrackPopupMenuEx")
	procSetForegroundWindow     = user32.NewProc("SetForegroundWindow")
	procGetCursorPos            = user32.NewProc("GetCursorPos")
	procShellNotifyIconW        = shell32.NewProc("Shell_NotifyIconW")
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type point struct {
	x, y int32
}

type msg struct {
	hWnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             syscall.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            syscall.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     syscall.Handle
}

var (
	instancesMu sync.RWMutex
	instances   = map[syscall.Handle]*Tray{}
	wndProcCB   = syscall.NewCallback(wndProc)
)

func Start(cfg Config) (*Tray, error) {
	if cfg.Tooltip == "" {
		cfg.Tooltip = "Darius DB"
	}
	t := &Tray{
		cfg:     cfg,
		tooltip: cfg.Tooltip,
		done:    make(chan struct{}),
	}
	ready := make(chan error, 1)
	go t.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return t, nil
}

func (t *Tray) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className := "DariusDBTray-" + t.cfg.Tag
	if t.cfg.Tag == "" {
		className = "DariusDBTray"
	}
	classPtr, _ := windows.UTF16PtrFromString(className)

	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   wndProcCB,
		hInstance:     syscall.Handle(hInst),
		lpszClassName: classPtr,
	}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		ready <- fmt.Errorf("RegisterClassExW: %w", err)
		return
	}
	defer procUnregisterClassW.Call(uintptr(unsafe.Pointer(classPtr)), hInst)

	tbName, _ := windows.UTF16PtrFromString("TaskbarCreated")
	tbMsg, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(tbName)))
	t.taskbarMsg = uint32(tbMsg)

	if t.cfg.Tag != "" {
		showName, _ := windows.UTF16PtrFromString("DariusDBShow-" + t.cfg.Tag)
		sMsg, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(showName)))
		t.showMsg = uint32(sMsg)
	}

	titlePtr, _ := windows.UTF16PtrFromString("Darius DB Tray")
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		0,
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW: %w", err)
		return
	}
	t.hwnd = syscall.Handle(hwnd)

	instancesMu.Lock()
	instances[t.hwnd] = t
	instancesMu.Unlock()
	defer func() {
		instancesMu.Lock()
		delete(instances, t.hwnd)
		instancesMu.Unlock()
	}()

	if len(t.cfg.IconPNG) > 0 {
		cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
		cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
		if cx == 0 {
			cx = 32
		}
		if cy == 0 {
			cy = 32
		}
		hicon, _, _ := procCreateIconFromResEx.Call(
			uintptr(unsafe.Pointer(&t.cfg.IconPNG[0])),
			uintptr(len(t.cfg.IconPNG)),
			1,
			0x00030000,
			cx, cy,
			0,
		)
		t.hicon = syscall.Handle(hicon)
	}
	defer func() {
		if t.hicon != 0 {
			procDestroyIcon.Call(uintptr(t.hicon))
		}
	}()

	t.addIcon()
	ready <- nil

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (t *Tray) makeNID() notifyIconDataW {
	nid := notifyIconDataW{
		cbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:             t.hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayIcon,
		hIcon:            t.hicon,
	}
	t.mu.Lock()
	tip := t.tooltip
	t.mu.Unlock()
	if utf16, err := windows.UTF16FromString(tip); err == nil {
		copy(nid.szTip[:len(nid.szTip)-1], utf16)
	}
	return nid
}

func (t *Tray) addIcon() {
	nid := t.makeNID()
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) modifyIcon() {
	nid := t.makeNID()
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) deleteIcon() {
	nid := notifyIconDataW{
		cbSize: uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:   t.hwnd,
		uID:    1,
	}
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) SetTooltip(tip string) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.tooltip = tip
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmUpdateTip, 0, 0)
	}
}

func (t *Tray) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
		<-t.done
	}
}

func (t *Tray) showMenu() {
	if t.cfg.BuildMenu == nil {
		return
	}
	items := t.cfg.BuildMenu()
	if len(items) == 0 {
		return
	}
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	actions := map[uintptr]func(){}
	for i, it := range items {
		id := uintptr(i + 100)
		if it.Separator {
			procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if it.Disabled {
			flags |= mfDisabled | mfGrayed
		}
		lbl, _ := windows.UTF16PtrFromString(it.Label)
		procAppendMenuW.Call(hMenu, flags, id, uintptr(unsafe.Pointer(lbl)))
		if it.Default {
			procSetMenuDefaultItem.Call(hMenu, id, 0)
		}
		if it.Action != nil {
			actions[id] = it.Action
		}
	}

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(t.hwnd))

	cmd, _, _ := procTrackPopupMenuEx.Call(
		hMenu,
		tpmReturnCmd|tpmNonotify|tpmRightButton|tpmBottomAlign|tpmLeftAlign,
		uintptr(pt.x),
		uintptr(pt.y),
		uintptr(t.hwnd),
		0,
	)
	procPostMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)

	if fn := actions[cmd]; fn != nil {
		go fn()
	}
}

func wndProc(hwnd syscall.Handle, message uint32, wParam, lParam uintptr) uintptr {
	instancesMu.RLock()
	t := instances[hwnd]
	instancesMu.RUnlock()

	if t != nil {
		switch message {
		case wmTrayIcon:
			ev := uint32(lParam & 0xFFFF)
			switch ev {
			case wmLButtonUp, wmLButtonDbl:
				if t.cfg.OnShow != nil {
					go t.cfg.OnShow()
				}
				return 0
			case wmRButtonUp, wmContextMenu:
				t.showMenu()
				return 0
			}
		case wmUpdateTip:
			t.modifyIcon()
			return 0
		case wmClose:
			t.deleteIcon()
			procDestroyWindow.Call(uintptr(hwnd))
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		default:
			if t.taskbarMsg != 0 && message == t.taskbarMsg {
				t.addIcon()
				return 0
			}
			if t.showMsg != 0 && message == t.showMsg {
				if t.cfg.OnShow != nil {
					go t.cfg.OnShow()
				}
				return 0
			}
		}
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return ret
}
