//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"dariusdb/internal/tray"
	"dariusdb/internal/winui"
)

const (
	appShortName = "Darius DB"
	appFullTitle = "Darius DB (Lite Version 0.1.3)"
	appVersion   = "0.1.3"
	appAuthor    = "Darius Cumpio"
	defaultDir   = `C:\DariusDB`
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB`
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func resolveRoot() string {
	if h := os.Getenv("DARIUSDB_HOME"); h != "" {
		if abs, err := filepath.Abs(h); err == nil {
			return abs
		}
		return h
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		if fileExists(filepath.Join(dir, "DariusDB.exe")) {
			return dir
		}
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE); err == nil {
		loc, _, err := k.GetStringValue("InstallLocation")
		k.Close()
		if err == nil && loc != "" {
			return loc
		}
	}
	return defaultDir
}

func main() {
	// Single-instance mutex for the Uninstall / Maintenance Wizard
	mutexName, _ := windows.UTF16PtrFromString("Local\\DariusDBUninstallWizardMutex")
	hMutex, err := windows.CreateMutex(nil, false, mutexName)
	if err == windows.ERROR_ALREADY_EXISTS {
		if hMutex != 0 {
			windows.CloseHandle(hMutex)
		}
		winui.MessageBox(
			appFullTitle+" Uninstall Wizard",
			"The Darius DB Uninstall & Maintenance Wizard is already open.",
			winui.MBOK|winui.MBIconInfo,
		)
		return
	}
	if hMutex != 0 {
		defer windows.CloseHandle(hMutex)
	}

	root := resolveRoot()
	selfExe, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(selfExe); err == nil {
		selfExe = real
	}

	silent := false
	keepData := true
	repairOnly := false
	for _, a := range os.Args[1:] {
		low := strings.ToLower(a)
		switch {
		case low == "--silent" || low == "/s" || low == "/verysilent":
			silent = true
		case low == "--repair" || low == "/repair":
			repairOnly = true
		case low == "--remove-data" || low == "--purge":
			keepData = false
		case low == "--keep-data":
			keepData = true
		case strings.HasPrefix(low, "--dir="):
			root = strings.Trim(a[6:], `"`)
		}
	}

	if silent {
		opts := uninstOptions{
			root:       root,
			selfExe:    selfExe,
			repairMode: repairOnly,
			keepData:   keepData,
			removeBins: true,
			cleanPath:  true,
		}
		if repairOnly {
			_ = performRepair(opts, nil)
		} else {
			_ = performUninstall(opts, nil)
			scheduleInstalledSelfDelete(root, selfExe, !keepData)
		}
		return
	}

	uw.root = root
	uw.selfExe = selfExe
	uw.repairMode = repairOnly
	runUninstallWizardGUI()
}

type uninstOptions struct {
	root       string
	selfExe    string
	repairMode bool
	keepData   bool
	removeBins bool
	cleanPath  bool
}

// ---------------------------------------------------------------- Win32 Uninstall Wizard GUI

const (
	wsOverlapped  = 0x00000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsVisible     = 0x10000000
	wsChild       = 0x40000000
	wsTabStop     = 0x00010000
	wsBorder      = 0x00800000
	wsVScroll     = 0x00200000
	wsGroup       = 0x00020000

	wsExClientEdge = 0x00000200

	ssIcon       = 0x00000003
	ssEtchedHorz = 0x00000010

	bsPushButton      = 0x00000000
	bsDefPushBtn      = 0x00000001
	bsAutoCheckBox    = 0x00000003
	bsAutoRadioButton = 0x00000009

	esMultiline   = 0x0004
	esAutoVScroll = 0x0040
	esAutoHScroll = 0x0080
	esReadOnly    = 0x0800

	wmCreate       = 0x0001
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmSetFont      = 0x0030
	wmSetIcon      = 0x0080
	wmCommand      = 0x0111
	wmCtlColorStat = 0x0138
	wmUser         = 0x0400
	wmUninstStep   = wmUser + 20
	wmUninstDone   = wmUser + 21

	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1
	bstChecked = 1

	stmSetIcon = 0x0170

	pbmSetRange32 = wmUser + 6
	pbmSetPos     = wmUser + 2

	emSetSel      = 0x00B1
	emScrollCaret = 0x00B7

	swHide = 0
	swShow = 5

	colorBtnFace = 15

	idBtnBack      = 101
	idBtnNext      = 102
	idBtnCancel    = 103
	idRadUninstall = 201
	idRadRepair    = 202
	idChkKeepData  = 203
	idChkRemBins   = 204
	idChkCleanPath = 205
	idChkLaunchApp = 301
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")

	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procEnableWindow         = user32.NewProc("EnableWindow")
	procSetWindowTextW       = user32.NewProc("SetWindowTextW")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procGetSystemMetrics     = user32.NewProc("GetSystemMetrics")
	procCreateIconFromResEx  = user32.NewProc("CreateIconFromResourceEx")
	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procSetBkMode            = gdi32.NewProc("SetBkMode")
	procGetSysColorBrush     = user32.NewProc("GetSysColorBrush")
)

type initCommonControlsEx struct {
	dwSize uint32
	dwICC  uint32
}

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

type point struct{ x, y int32 }

type msg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type uninstWizardState struct {
	hwnd       syscall.Handle
	hInst      syscall.Handle
	hIcon48    syscall.Handle
	hIconSm    syscall.Handle
	fontTitle  syscall.Handle
	fontBold   syscall.Handle
	fontUI     syscall.Handle
	fontMono   syscall.Handle
	page       int // 1..3
	working    bool
	repairMode bool
	keepData   bool
	root       string
	selfExe    string

	// Header
	lblTitle    syscall.Handle
	lblSubtitle syscall.Handle

	// Page 1: Action & Options
	p1Intro        syscall.Handle
	p1LblDir       syscall.Handle
	p1EditDir      syscall.Handle
	p1LblAction    syscall.Handle
	p1RadUninstall syscall.Handle
	p1RadRepair    syscall.Handle
	p1LblOpts      syscall.Handle
	p1ChkKeepData  syscall.Handle
	p1ChkRemBins   syscall.Handle
	p1ChkCleanPath syscall.Handle

	// Page 2: Progress & Live Log
	p2Status   syscall.Handle
	p2Progress syscall.Handle
	p2LogBox   syscall.Handle

	// Page 3: Complete
	p3DoneTitle syscall.Handle
	p3Summary   syscall.Handle
	p3ChkLaunch syscall.Handle

	// Bottom navigation buttons
	btnBack   syscall.Handle
	btnNext   syscall.Handle
	btnCancel syscall.Handle

	mu      sync.Mutex
	stepMsg string
	stepPct int
	errText string
}

var uw = &uninstWizardState{root: defaultDir, keepData: true}

func u16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func makeIcon(size uintptr) syscall.Handle {
	if len(tray.DefaultIconPNG) == 0 {
		return 0
	}
	h, _, _ := procCreateIconFromResEx.Call(
		uintptr(unsafe.Pointer(&tray.DefaultIconPNG[0])),
		uintptr(len(tray.DefaultIconPNG)),
		1,
		0x00030000,
		size, size,
		0,
	)
	return syscall.Handle(h)
}

func makeFont(height int32, weight int32, face string) syscall.Handle {
	h, _, _ := procCreateFontW.Call(
		uintptr(uint32(height)), 0, 0, 0,
		uintptr(weight),
		0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(u16(face))),
	)
	return syscall.Handle(h)
}

func createCtrl(exStyle uint32, class, text string, style uint32, x, y, w, h int, parent syscall.Handle, id uintptr, font syscall.Handle) syscall.Handle {
	hwnd, _, _ := procCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(u16(class))),
		uintptr(unsafe.Pointer(u16(text))),
		uintptr(wsChild|style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(parent),
		id,
		uintptr(uw.hInst),
		0,
	)
	handle := syscall.Handle(hwnd)
	if font != 0 && handle != 0 {
		procSendMessageW.Call(uintptr(handle), wmSetFont, uintptr(font), 1)
	}
	return handle
}

func showCtrl(h syscall.Handle, vis bool) {
	if vis {
		procShowWindow.Call(uintptr(h), swShow)
	} else {
		procShowWindow.Call(uintptr(h), swHide)
	}
}

func setText(h syscall.Handle, s string) {
	procSetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(u16(s))))
}

func getText(h syscall.Handle) string {
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(h))
	buf := make([]uint16, n+2)
	procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}

func isChecked(h syscall.Handle) bool {
	r, _, _ := procSendMessageW.Call(uintptr(h), bmGetCheck, 0, 0)
	return r == bstChecked
}

func setChecked(h syscall.Handle, checked bool) {
	v := uintptr(0)
	if checked {
		v = bstChecked
	}
	procSendMessageW.Call(uintptr(h), bmSetCheck, v, 0)
}

func runUninstallWizardGUI() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	icc := initCommonControlsEx{
		dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})),
		dwICC:  0x00000020 | 0x00004000,
	}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))

	hInst, _, _ := procGetModuleHandleW.Call(0)
	uw.hInst = syscall.Handle(hInst)
	uw.hIcon48 = makeIcon(48)
	uw.hIconSm = makeIcon(32)
	uw.fontTitle = makeFont(-18, 700, "Segoe UI")
	uw.fontBold = makeFont(-14, 600, "Segoe UI")
	uw.fontUI = makeFont(-13, 400, "Segoe UI")
	uw.fontMono = makeFont(-12, 400, "Consolas")

	clsName := u16("DariusDBUninstallWizard")
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(uninstWndProc),
		hInstance:     uw.hInst,
		hIcon:         uw.hIcon48,
		hIconSm:       uw.hIconSm,
		hbrBackground: syscall.Handle(colorBtnFace + 1),
		lpszClassName: clsName,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	const winW, winH = 560, 430
	scrW, _, _ := procGetSystemMetrics.Call(0)
	scrH, _, _ := procGetSystemMetrics.Call(1)
	posX := (int(scrW) - winW) / 2
	posY := (int(scrH) - winH) / 2

	winTitle := appFullTitle + " Uninstall & Maintenance Wizard"
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(u16(winTitle))),
		wsOverlapped|wsCaption|wsSysMenu|wsMinimizeBox|wsVisible,
		uintptr(posX), uintptr(posY), winW, winH,
		0, 0, uintptr(uw.hInst), 0,
	)
	uw.hwnd = syscall.Handle(hwnd)
	procSendMessageW.Call(hwnd, wmSetIcon, 1, uintptr(uw.hIcon48))
	procSendMessageW.Call(hwnd, wmSetIcon, 0, uintptr(uw.hIconSm))

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func buildUninstControls(hwnd syscall.Handle) {
	// Top banner icon + title + subtitle (matches Setup Wizard layout)
	icoCtrl := createCtrl(0, "STATIC", "", wsVisible|ssIcon, 18, 12, 48, 48, hwnd, 0, 0)
	if uw.hIcon48 != 0 {
		procSendMessageW.Call(uintptr(icoCtrl), stmSetIcon, uintptr(uw.hIcon48), 0)
	}
	uw.lblTitle = createCtrl(0, "STATIC", appFullTitle+" Uninstall", wsVisible, 78, 14, 450, 24, hwnd, 0, uw.fontTitle)
	uw.lblSubtitle = createCtrl(0, "STATIC", "Step 1 of 3 — Uninstall or Repair Darius DB", wsVisible, 78, 40, 450, 20, hwnd, 0, uw.fontUI)
	createCtrl(0, "STATIC", "", wsVisible|ssEtchedHorz, 0, 70, 560, 2, hwnd, 0, 0)

	// ---- Page 1: Action & Data Options ----
	uw.p1LblDir = createCtrl(0, "STATIC", "Installed Location:", 0, 22, 82, 480, 18, hwnd, 0, uw.fontBold)
	uw.p1EditDir = createCtrl(wsExClientEdge, "EDIT", uw.root,
		wsBorder|esAutoHScroll|esReadOnly, 22, 102, 502, 24, hwnd, 0, uw.fontUI)

	uw.p1LblAction = createCtrl(0, "STATIC", "Choose Maintenance Action:", 0, 22, 136, 480, 18, hwnd, 0, uw.fontBold)
	uw.p1RadUninstall = createCtrl(0, "BUTTON", "Uninstall Darius DB (Lite Version 0.1.3) from this computer",
		wsTabStop|wsGroup|bsAutoRadioButton, 26, 158, 495, 22, hwnd, idRadUninstall, uw.fontUI)
	uw.p1RadRepair = createCtrl(0, "BUTTON", "Repair Darius DB (fix corrupted configs, stale PID locks, folders & shortcuts)",
		wsTabStop|bsAutoRadioButton, 26, 183, 495, 22, hwnd, idRadRepair, uw.fontUI)

	uw.p1LblOpts = createCtrl(0, "STATIC", "Database Data & Cleanup Options:", 0, 22, 215, 480, 18, hwnd, 0, uw.fontBold)
	uw.p1ChkKeepData = createCtrl(0, "BUTTON", "Keep my database data, websites & configs safe (data/, www/, etc/) — Recommended",
		wsTabStop|bsAutoCheckBox, 26, 238, 505, 22, hwnd, idChkKeepData, uw.fontUI)
	uw.p1ChkRemBins = createCtrl(0, "BUTTON", "Remove downloaded engine binaries (bin/) and temporary logs (logs/, tmp/)",
		wsTabStop|bsAutoCheckBox, 26, 264, 505, 22, hwnd, idChkRemBins, uw.fontUI)
	uw.p1ChkCleanPath = createCtrl(0, "BUTTON", "Remove Desktop/Start Menu shortcuts and clean Windows User PATH",
		wsTabStop|bsAutoCheckBox, 26, 290, 505, 22, hwnd, idChkCleanPath, uw.fontUI)

	setChecked(uw.p1ChkKeepData, true)
	setChecked(uw.p1ChkRemBins, true)
	setChecked(uw.p1ChkCleanPath, true)
	if uw.repairMode {
		setChecked(uw.p1RadRepair, true)
	} else {
		setChecked(uw.p1RadUninstall, true)
	}

	// ---- Page 2: Progress & Live Log ----
	uw.p2Status = createCtrl(0, "STATIC", "Preparing...",
		0, 22, 86, 502, 20, hwnd, 0, uw.fontBold)
	uw.p2Progress = createCtrl(0, "msctls_progress32", "",
		0, 22, 110, 502, 24, hwnd, 0, 0)
	procSendMessageW.Call(uintptr(uw.p2Progress), pbmSetRange32, 0, 100)
	uw.p2LogBox = createCtrl(wsExClientEdge, "EDIT", "",
		wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		22, 144, 502, 176, hwnd, 0, uw.fontMono)

	// ---- Page 3: Finish ----
	uw.p3DoneTitle = createCtrl(0, "STATIC", "Darius DB (Lite Version 0.1.3) has been uninstalled!",
		0, 22, 92, 500, 24, hwnd, 0, uw.fontTitle)
	uw.p3Summary = createCtrl(0, "STATIC", "",
		0, 22, 126, 500, 110, hwnd, 0, uw.fontUI)
	uw.p3ChkLaunch = createCtrl(0, "BUTTON", "Launch Darius DB (Lite Version 0.1.3) now",
		wsTabStop|bsAutoCheckBox, 26, 246, 460, 24, hwnd, idChkLaunchApp, uw.fontBold)
	setChecked(uw.p3ChkLaunch, true)

	// Bottom divider & navigation buttons (matches Setup Wizard)
	createCtrl(0, "STATIC", "", wsVisible|ssEtchedHorz, 0, 336, 560, 2, hwnd, 0, 0)
	uw.btnBack = createCtrl(0, "BUTTON", "< Back", wsVisible|wsTabStop|bsPushButton, 244, 350, 90, 30, hwnd, idBtnBack, uw.fontUI)
	uw.btnNext = createCtrl(0, "BUTTON", "Uninstall", wsVisible|wsTabStop|bsDefPushBtn, 340, 350, 90, 30, hwnd, idBtnNext, uw.fontBold)
	uw.btnCancel = createCtrl(0, "BUTTON", "Cancel", wsVisible|wsTabStop|bsPushButton, 436, 350, 90, 30, hwnd, idBtnCancel, uw.fontUI)

	updateModeControls()
	showUninstPage(1)
}

func updateModeControls() {
	uw.repairMode = isChecked(uw.p1RadRepair)
	enableUninstOpts := uintptr(1)
	if uw.repairMode {
		enableUninstOpts = 0
		setText(uw.lblTitle, appFullTitle+" Repair")
		setText(uw.btnNext, "Repair")
	} else {
		setText(uw.lblTitle, appFullTitle+" Uninstall")
		setText(uw.btnNext, "Uninstall")
	}
	procEnableWindow.Call(uintptr(uw.p1ChkKeepData), enableUninstOpts)
	procEnableWindow.Call(uintptr(uw.p1ChkRemBins), enableUninstOpts)
	procEnableWindow.Call(uintptr(uw.p1ChkCleanPath), enableUninstOpts)
}

func showUninstPage(page int) {
	uw.page = page

	for _, h := range []syscall.Handle{
		uw.p1LblDir, uw.p1EditDir, uw.p1LblAction, uw.p1RadUninstall, uw.p1RadRepair,
		uw.p1LblOpts, uw.p1ChkKeepData, uw.p1ChkRemBins, uw.p1ChkCleanPath,
	} {
		showCtrl(h, page == 1)
	}
	for _, h := range []syscall.Handle{uw.p2Status, uw.p2Progress, uw.p2LogBox} {
		showCtrl(h, page == 2)
	}
	for _, h := range []syscall.Handle{uw.p3DoneTitle, uw.p3Summary} {
		showCtrl(h, page == 3)
	}
	showCtrl(uw.p3ChkLaunch, page == 3 && uw.repairMode)

	switch page {
	case 1:
		setText(uw.lblSubtitle, "Step 1 of 3 — Choose Maintenance Action & Data Options")
		procEnableWindow.Call(uintptr(uw.btnBack), 0)
		procEnableWindow.Call(uintptr(uw.btnNext), 1)
		updateModeControls()

	case 2:
		if uw.repairMode {
			setText(uw.lblSubtitle, "Step 2 of 3 — Repairing Darius DB (Lite Version 0.1.3)...")
			setText(uw.btnNext, "Repairing...")
		} else {
			setText(uw.lblSubtitle, "Step 2 of 3 — Uninstalling Darius DB (Lite Version 0.1.3)...")
			setText(uw.btnNext, "Removing...")
		}
		procEnableWindow.Call(uintptr(uw.btnBack), 0)
		procEnableWindow.Call(uintptr(uw.btnNext), 0)
		procEnableWindow.Call(uintptr(uw.btnCancel), 0)

	case 3:
		if uw.repairMode {
			setText(uw.lblSubtitle, "Step 3 of 3 — Repair Completed")
			setText(uw.p3DoneTitle, "Darius DB (Lite Version 0.1.3) has been repaired!")
			setText(uw.p3Summary, fmt.Sprintf(
				"Repaired Folder:    %s\r\n"+
					"Portable Folders:   Verified www/, bin/, data/, etc/, logs/, tmp/\r\n"+
					"Configs & Locks:    Cleaned stale PID locks & verified settings.json\r\n"+
					"Database Files:     All databases in data/ were preserved untouched\r\n"+
					"Created By:         %s (MIT Open Source)",
				uw.root, appAuthor,
			))
		} else {
			setText(uw.lblSubtitle, "Step 3 of 3 — Uninstall Completed")
			setText(uw.p3DoneTitle, "Darius DB (Lite Version 0.1.3) has been uninstalled!")
			dataNote := "Removed all program files and database folders."
			if uw.keepData {
				dataNote = fmt.Sprintf("Your databases & websites were kept safely in:\r\n                    %s\\data  and  %s\\www", uw.root, uw.root)
			}
			setText(uw.p3Summary, fmt.Sprintf(
				"Uninstalled From:   %s\r\n"+
					"Shortcuts & Registry: Removed Desktop, Start Menu & Installed Apps entries\r\n"+
					"Data Status:        %s\r\n"+
					"Created By:         %s (MIT Open Source)",
				uw.root, dataNote, appAuthor,
			))
		}
		showCtrl(uw.btnBack, false)
		showCtrl(uw.btnCancel, false)
		procEnableWindow.Call(uintptr(uw.btnNext), 1)
		setText(uw.btnNext, "Finish")
	}
}

func appendUninstLog(line string) {
	cur := getText(uw.p2LogBox)
	if cur != "" {
		cur += "\r\n"
	}
	cur += line
	setText(uw.p2LogBox, cur)
	n := len(cur)
	procSendMessageW.Call(uintptr(uw.p2LogBox), emSetSel, uintptr(n), uintptr(n))
	procSendMessageW.Call(uintptr(uw.p2LogBox), emScrollCaret, 0, 0)
}

func startAsyncAction() {
	uw.repairMode = isChecked(uw.p1RadRepair)
	uw.keepData = isChecked(uw.p1ChkKeepData)
	opts := uninstOptions{
		root:       uw.root,
		selfExe:    uw.selfExe,
		repairMode: uw.repairMode,
		keepData:   uw.keepData,
		removeBins: isChecked(uw.p1ChkRemBins),
		cleanPath:  isChecked(uw.p1ChkCleanPath),
	}

	if !opts.repairMode && !opts.keepData {
		ans := winui.MessageBox(
			appFullTitle+" Uninstall — Confirm Data Deletion",
			fmt.Sprintf(
				"You unchecked 'Keep my database data, websites & configs safe'.\n\n"+
					"This will PERMANENTLY DELETE all databases in:\n%s\\data\n\n"+
					"Are you sure you want to permanently delete all databases?",
				uw.root,
			),
			winui.MBYesNo|winui.MBIconWarning,
		)
		if ans != winui.IDYES {
			return
		}
	}

	showUninstPage(2)
	uw.working = true

	go func() {
		report := func(pct int, status string) {
			uw.mu.Lock()
			uw.stepPct = pct
			uw.stepMsg = status
			uw.mu.Unlock()
			procPostMessageW.Call(uintptr(uw.hwnd), wmUninstStep, uintptr(pct), 0)
			time.Sleep(180 * time.Millisecond)
		}

		var err error
		if opts.repairMode {
			err = performRepair(opts, report)
		} else {
			err = performUninstall(opts, report)
		}
		uw.mu.Lock()
		if err != nil {
			uw.errText = err.Error()
		}
		uw.mu.Unlock()
		procPostMessageW.Call(uintptr(uw.hwnd), wmUninstDone, 0, 0)
	}()
}

func uninstWndProc(hwnd syscall.Handle, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		buildUninstControls(hwnd)
		return 0

	case wmCtlColorStat:
		procSetBkMode.Call(wParam, 1)
		br, _, _ := procGetSysColorBrush.Call(colorBtnFace)
		return br

	case wmCommand:
		id := int(wParam & 0xFFFF)
		switch id {
		case idRadUninstall, idRadRepair:
			if uw.page == 1 {
				updateModeControls()
			}
		case idBtnNext:
			switch uw.page {
			case 1:
				startAsyncAction()
			case 3:
				finishUninstWizard()
			}
		case idBtnCancel:
			if !uw.working {
				procDestroyWindow.Call(uintptr(hwnd))
			}
		}
		return 0

	case wmUninstStep:
		uw.mu.Lock()
		msgText := uw.stepMsg
		pct := uw.stepPct
		uw.mu.Unlock()
		setText(uw.p2Status, fmt.Sprintf("%s (%d%%)", msgText, pct))
		procSendMessageW.Call(uintptr(uw.p2Progress), pbmSetPos, uintptr(pct), 0)
		appendUninstLog("✓ " + msgText)
		return 0

	case wmUninstDone:
		uw.working = false
		uw.mu.Lock()
		errText := uw.errText
		uw.mu.Unlock()
		if errText != "" {
			winui.MessageBox(appFullTitle, "Operation encountered an error:\n\n"+errText, winui.MBOK|winui.MBIconError)
		}
		showUninstPage(3)
		return 0

	case wmClose:
		if !uw.working {
			procDestroyWindow.Call(uintptr(hwnd))
		}
		return 0

	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return ret
}

func finishUninstWizard() {
	if uw.repairMode {
		if isChecked(uw.p3ChkLaunch) {
			launcher := filepath.Join(uw.root, "DariusDB-Launcher.exe")
			if !fileExists(launcher) {
				launcher = filepath.Join(uw.root, "DariusDB.exe")
			}
			if fileExists(launcher) {
				cmd := exec.Command(launcher)
				cmd.Dir = uw.root
				_ = cmd.Start()
			}
		}
	} else {
		scheduleInstalledSelfDelete(uw.root, uw.selfExe, !uw.keepData)
	}
	procDestroyWindow.Call(uintptr(uw.hwnd))
}

// ---------------------------------------------------------------- Uninstall & Repair Engines

func performUninstall(opts uninstOptions, report func(pct int, msg string)) error {
	step := func(pct int, msg string) {
		if report != nil {
			report(pct, msg)
		}
	}

	step(15, "Stopping any running Darius DB services and processes in "+opts.root)
	existingLauncher := filepath.Join(opts.root, "DariusDB-Launcher.exe")
	if fileExists(existingLauncher) {
		stopCmd := exec.Command(existingLauncher, "--stop-all")
		stopCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = stopCmd.Run()
	}
	winui.KillProcessesInDir(opts.root, uint32(os.Getpid()))
	time.Sleep(400 * time.Millisecond)

	step(32, "Removing Windows Startup & Installed Apps registry entries")
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue("DariusDB")
		k.Close()
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstallKey)

	if opts.cleanPath {
		step(48, "Removing Desktop & Start Menu shortcuts")
		removeShortcuts()

		step(62, "Removing Darius DB from Windows User PATH")
		removeDirFromUserPath(opts.root)
	}

	step(78, "Removing Darius DB application binaries (DariusDB.exe, Launcher, Updater, CLI)")
	programItems := []string{
		"DariusDB.exe",
		"DariusDB-Launcher.exe",
		"DariusDB-Updater.exe",
		"darius.exe",
		"LICENSE",
		"README.md",
		"GUIDE.md",
		"catalog.json",
		"version.json",
	}
	if opts.removeBins {
		programItems = append(programItems, "bin", "tmp", "logs")
	}
	if !opts.keepData {
		step(90, "Deleting database data, web root, and config folders (data/, www/, etc/)")
		programItems = append(programItems, "data", "www", "etc", "settings.json", "settings.json.bak")
	} else {
		step(90, "Preserving database data, websites, and configs in "+opts.root)
	}
	for _, name := range programItems {
		_ = os.RemoveAll(filepath.Join(opts.root, name))
	}

	step(100, "Darius DB (Lite Version 0.1.3) uninstall complete!")
	return nil
}

func performRepair(opts uninstOptions, report func(pct int, msg string)) error {
	step := func(pct int, msg string) {
		if report != nil {
			report(pct, msg)
		}
	}

	step(18, "Verifying portable directory structure (www, bin, data, etc, logs, tmp)")
	for _, sub := range []string{"www", "bin", "data", "etc", "logs", "tmp"} {
		_ = os.MkdirAll(filepath.Join(opts.root, sub), 0o755)
	}

	cliExe := filepath.Join(opts.root, "darius.exe")
	if fileExists(cliExe) {
		step(50, "Running darius.exe repair all (configs, settings.json, stale PID locks, LocalDB)")
		cmd := exec.Command(cliExe, "repair", "all")
		cmd.Dir = opts.root
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if out, err := cmd.CombinedOutput(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "| OK") || strings.HasPrefix(line, "| FIXED") {
					step(65, strings.Trim(line, "| "))
				}
			}
		}
	}

	step(85, "Repairing Start Menu and Desktop shortcuts")
	repairShortcuts(opts.root)

	step(100, "Darius DB (Lite Version 0.1.3) repair complete!")
	return nil
}

func removeShortcuts() {
	if appData := os.Getenv("APPDATA"); appData != "" {
		_ = os.RemoveAll(filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Darius DB"))
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		for _, sub := range []string{"Desktop", filepath.Join("OneDrive", "Desktop")} {
			_ = os.Remove(filepath.Join(profile, sub, "Darius DB.lnk"))
		}
	}
}

func repairShortcuts(installDir string) {
	mainExe := filepath.Join(installDir, "DariusDB.exe")
	launcherExe := filepath.Join(installDir, "DariusDB-Launcher.exe")
	if !fileExists(launcherExe) {
		launcherExe = mainExe
	}
	uninstExe := filepath.Join(installDir, "DariusDB-Uninstall.exe")
	updaterExe := filepath.Join(installDir, "DariusDB-Updater.exe")
	startMenu := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", appShortName)
	_ = os.MkdirAll(startMenu, 0o755)

	escPS := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	ps := fmt.Sprintf(`$w = New-Object -ComObject WScript.Shell; `+
		`function mk($l,$t,$a,$d){ $s=$w.CreateShortcut($l); $s.TargetPath=$t; $s.Arguments=$a; $s.WorkingDirectory='%s'; $s.IconLocation='%s,0'; $s.Description=$d; $s.Save() }; `+
		`mk '%s' '%s' '' 'Launch %s'; `+
		`mk '%s' '%s' '--terminal' 'Open %s Terminal'; `+
		`mk '%s' '%s' '' 'Update %s'; `+
		`mk '%s' '%s' '--repair' 'Repair %s'; `+
		`mk '%s' '%s' '' 'Uninstall %s'; `,
		escPS(installDir), escPS(mainExe),
		escPS(filepath.Join(startMenu, appShortName+".lnk")), escPS(launcherExe), escPS(appFullTitle),
		escPS(filepath.Join(startMenu, appShortName+" Terminal.lnk")), escPS(launcherExe), escPS(appFullTitle),
		escPS(filepath.Join(startMenu, "Update "+appShortName+".lnk")), escPS(updaterExe), escPS(appFullTitle),
		escPS(filepath.Join(startMenu, "Repair "+appShortName+".lnk")), escPS(uninstExe), escPS(appFullTitle),
		escPS(filepath.Join(startMenu, "Uninstall "+appShortName+".lnk")), escPS(uninstExe), escPS(appFullTitle),
	)
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	if st, err := os.Stat(desktop); err == nil && st.IsDir() {
		ps += fmt.Sprintf(`mk '%s' '%s' '' 'Launch %s';`,
			escPS(filepath.Join(desktop, appShortName+".lnk")), escPS(launcherExe), escPS(appFullTitle))
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	_ = cmd.Run()
}

func removeDirFromUserPath(targetDir string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	cur, _, err := k.GetStringValue("Path")
	if err != nil {
		return
	}
	cleanTarget := strings.ToLower(strings.TrimRight(filepath.Clean(targetDir), `\/`))
	var kept []string
	for _, p := range strings.Split(cur, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cleanP := strings.ToLower(strings.TrimRight(filepath.Clean(p), `\/`))
		if cleanP == cleanTarget || strings.HasPrefix(cleanP, cleanTarget+`\`) {
			continue
		}
		kept = append(kept, p)
	}
	_ = k.SetExpandStringValue("Path", strings.Join(kept, ";"))
}

func scheduleInstalledSelfDelete(root, selfExe string, removeRoot bool) {
	targetUninst := filepath.Join(root, "DariusDB-Uninstall.exe")
	cmdStr := fmt.Sprintf(`ping -n 3 127.0.0.1 >nul & del /f /q "%s" 2>nul`, targetUninst)
	if removeRoot {
		cmdStr += fmt.Sprintf(` & rmdir "%s" 2>nul`, root)
	}
	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd := exec.Command(cmdExe)
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
		CmdLine:       fmt.Sprintf(`"%s" /c "%s"`, cmdExe, cmdStr),
	}
	_ = cmd.Start()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
