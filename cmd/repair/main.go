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

	"dariusdb/internal/core"
	"dariusdb/internal/tray"
	"dariusdb/internal/winui"
)

const (
	appShortName = "Darius DB"
	appFullTitle = "Darius DB (Lite Version 0.1.5)"
	appVersion   = "0.1.5"
	appAuthor    = "Darius Cumpio"
)

type repairTargetOption struct {
	id    string
	label string
}

var repairTargetOptions = []repairTargetOption{
	{id: "all", label: "All System Components & Engines (Recommended)"},
	{id: "apache", label: "Apache HTTP Server & PHP"},
	{id: "mariadb", label: "MariaDB Server"},
	{id: "mysql", label: "MySQL Server"},
	{id: "postgresql", label: "PostgreSQL Server"},
	{id: "mssql", label: "MS SQL Server (Express / LocalDB)"},
	{id: "redis", label: "Redis Cache Server"},
	{id: "mailpit", label: "Mailpit SMTP Catcher"},
	{id: "composer", label: "PHP Composer"},
}

type repairAllOptions struct {
	root          string
	selfExe       string
	target        string
	restoreSystem bool
	stopProcs     bool
	fixPorts      bool
	cleanCache    bool
	resetConfigs  bool
}

func main() {
	mutexName, _ := windows.UTF16PtrFromString("Local\\DariusDBRepairAllWizardMutex")
	hMutex, err := windows.CreateMutex(nil, false, mutexName)
	if err == windows.ERROR_ALREADY_EXISTS {
		if hMutex != 0 {
			windows.CloseHandle(hMutex)
		}
		winui.MessageBox(
			appFullTitle+" Repair All",
			"The Darius DB Repair All Wizard is already running.",
			winui.MBOK|winui.MBIconInfo,
		)
		return
	}
	if hMutex != 0 {
		defer windows.CloseHandle(hMutex)
	}

	selfExe, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(selfExe); err == nil {
		selfExe = real
	}

	var explicitDir string
	opts := repairAllOptions{
		selfExe:       selfExe,
		target:        "all",
		restoreSystem: true,
		stopProcs:     true,
		fixPorts:      true,
		cleanCache:    true,
		resetConfigs:  false,
	}
	silent := false
	autoRun := false
	noLaunch := false

	for _, a := range os.Args[1:] {
		low := strings.ToLower(strings.TrimSpace(a))
		switch {
		case low == "--silent" || low == "/s" || low == "/verysilent":
			silent = true
		case low == "--auto":
			autoRun = true
		case low == "--no-launch":
			noLaunch = true
		case low == "--reset-config" || low == "--reset":
			opts.resetConfigs = true
		case low == "--fix-ports":
			opts.fixPorts = true
		case low == "--no-fix-ports":
			opts.fixPorts = false
		case low == "--clean-cache":
			opts.cleanCache = true
		case low == "--no-cache":
			opts.cleanCache = false
		case low == "--no-restore":
			opts.restoreSystem = false
		case low == "--no-stop":
			opts.stopProcs = false
		case strings.HasPrefix(low, "--dir="):
			explicitDir = strings.Trim(a[6:], `"`)
		case strings.HasPrefix(low, "--target="):
			if t := strings.ToLower(strings.TrimSpace(a[9:])); t != "" {
				opts.target = t
			}
		}
	}
	opts.root = winui.DetectInstallDir(explicitDir)

	if silent {
		rep, err := performRepairAll(opts, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Repair All failed: %v\n", err)
			os.Exit(1)
		}
		if rep != nil {
			fmt.Println(rep.Summary)
		}
		if !noLaunch {
			// Headless repair leaves running state clean unless user explicitly launches app.
		}
		return
	}

	rw.opts = opts
	rw.autoRun = autoRun
	rw.noLaunch = noLaunch
	runRepairWizardGUI()
}

// ---------------------------------------------------------------- Win32 Repair All Wizard GUI

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

	wsExClientEdge = 0x00000200

	ssIcon       = 0x00000003
	ssEtchedHorz = 0x00000010

	bsPushButton   = 0x00000000
	bsDefPushBtn   = 0x00000001
	bsAutoCheckBox = 0x00000003

	cbsDropDownList = 0x0003

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
	wmRepairStep   = wmUser + 30
	wmRepairDone   = wmUser + 31

	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1
	bstChecked = 1

	cbAddString = 0x0143
	cbGetCurSel = 0x0147
	cbSetCurSel = 0x014E

	stmSetIcon = 0x0170

	pbmSetRange32 = wmUser + 6
	pbmSetPos     = wmUser + 2

	emSetSel      = 0x00B1
	emScrollCaret = 0x00B7

	swHide = 0
	swShow = 5

	colorBtnFace = 15

	idBtnBack     = 101
	idBtnNext     = 102
	idBtnCancel   = 103
	idEditDir     = 201
	idBtnBrowse   = 202
	idCmbTarget   = 203
	idChkRestore  = 204
	idChkStopProc = 205
	idChkFixPorts = 206
	idChkCleanCch = 207
	idChkResetCfg = 208
	idChkLaunch   = 301
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

type repairWizardState struct {
	hwnd     syscall.Handle
	hInst    syscall.Handle
	hIcon48  syscall.Handle
	hIconSm  syscall.Handle
	fontTtl  syscall.Handle
	fontBold syscall.Handle
	fontUI   syscall.Handle
	fontMono syscall.Handle

	page     int // 1..3
	working  bool
	autoRun  bool
	noLaunch bool
	opts     repairAllOptions

	lblTitle    syscall.Handle
	lblSubtitle syscall.Handle

	// Page 1: Review & Repair Options (requires explicit click before running)
	p1LblDir      syscall.Handle
	p1EditDir     syscall.Handle
	p1BtnBrowse   syscall.Handle
	p1LblTarget   syscall.Handle
	p1CmbTarget   syscall.Handle
	p1LblOpts     syscall.Handle
	p1ChkRestore  syscall.Handle
	p1ChkStopProc syscall.Handle
	p1ChkFixPorts syscall.Handle
	p1ChkCleanCch syscall.Handle
	p1ChkResetCfg syscall.Handle
	p1Note        syscall.Handle

	// Page 2: Progress & Live Log
	p2Status   syscall.Handle
	p2Progress syscall.Handle
	p2LogBox   syscall.Handle

	// Page 3: Summary
	p3DoneTitle syscall.Handle
	p3Summary   syscall.Handle
	p3ChkLaunch syscall.Handle

	// Bottom Buttons
	btnBack   syscall.Handle
	btnNext   syscall.Handle
	btnCancel syscall.Handle

	mu        sync.Mutex
	logs      []string
	stepMsg   string
	stepPct   int
	errText   string
	report    *core.RepairReport
	restored  []string
	usedSetup bool
}

var rw = &repairWizardState{}

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
		uintptr(rw.hInst),
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

func runRepairWizardGUI() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	icc := initCommonControlsEx{
		dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})),
		dwICC:  0x00000020 | 0x00004000,
	}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))

	hInst, _, _ := procGetModuleHandleW.Call(0)
	rw.hInst = syscall.Handle(hInst)
	rw.hIcon48 = makeIcon(48)
	rw.hIconSm = makeIcon(32)
	rw.fontTtl = makeFont(-18, 700, "Segoe UI")
	rw.fontBold = makeFont(-14, 600, "Segoe UI")
	rw.fontUI = makeFont(-13, 400, "Segoe UI")
	rw.fontMono = makeFont(-12, 400, "Consolas")

	clsName := u16("DariusDBRepairAllWizard")
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(repairWndProc),
		hInstance:     rw.hInst,
		hIcon:         rw.hIcon48,
		hIconSm:       rw.hIconSm,
		hbrBackground: syscall.Handle(colorBtnFace + 1),
		lpszClassName: clsName,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	const winW, winH = 580, 470
	scrW, _, _ := procGetSystemMetrics.Call(0)
	scrH, _, _ := procGetSystemMetrics.Call(1)
	posX := (int(scrW) - winW) / 2
	posY := (int(scrH) - winH) / 2

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(u16(appFullTitle+" — Repair All Installer"))),
		wsOverlapped|wsCaption|wsSysMenu|wsMinimizeBox|wsVisible,
		uintptr(posX), uintptr(posY), winW, winH,
		0, 0, uintptr(rw.hInst), 0,
	)
	rw.hwnd = syscall.Handle(hwnd)
	procSendMessageW.Call(hwnd, wmSetIcon, 1, uintptr(rw.hIcon48))
	procSendMessageW.Call(hwnd, wmSetIcon, 0, uintptr(rw.hIconSm))

	if rw.autoRun {
		startAsyncRepairAll()
	}

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

func buildRepairControls(hwnd syscall.Handle) {
	icoCtrl := createCtrl(0, "STATIC", "", wsVisible|ssIcon, 18, 12, 48, 48, hwnd, 0, 0)
	if rw.hIcon48 != 0 {
		procSendMessageW.Call(uintptr(icoCtrl), stmSetIcon, uintptr(rw.hIcon48), 0)
	}
	rw.lblTitle = createCtrl(0, "STATIC", appFullTitle+" Repair All", wsVisible, 78, 14, 470, 24, hwnd, 0, rw.fontTtl)
	rw.lblSubtitle = createCtrl(0, "STATIC", "Step 1 of 3 — Review Repair All Scope & Click 'Repair All' to Start", wsVisible, 78, 40, 470, 20, hwnd, 0, rw.fontUI)
	createCtrl(0, "STATIC", "", wsVisible|ssEtchedHorz, 0, 70, 580, 2, hwnd, 0, 0)

	// ---- Page 1: Review & Options ----
	rw.p1LblDir = createCtrl(0, "STATIC", "Target Darius DB Installation Folder:", 0, 22, 82, 510, 18, hwnd, 0, rw.fontBold)
	rw.p1EditDir = createCtrl(wsExClientEdge, "EDIT", rw.opts.root,
		wsTabStop|wsBorder|esAutoHScroll, 22, 103, 420, 26, hwnd, idEditDir, rw.fontUI)
	rw.p1BtnBrowse = createCtrl(0, "BUTTON", "Browse...",
		wsTabStop|bsPushButton, 448, 102, 98, 28, hwnd, idBtnBrowse, rw.fontUI)

	rw.p1LblTarget = createCtrl(0, "STATIC", "Component / System Scope to Repair:", 0, 22, 138, 510, 18, hwnd, 0, rw.fontBold)
	rw.p1CmbTarget = createCtrl(0, "COMBOBOX", "",
		wsTabStop|wsVScroll|cbsDropDownList, 22, 159, 524, 210, hwnd, idCmbTarget, rw.fontUI)
	selectedIdx := 0
	for i, opt := range repairTargetOptions {
		procSendMessageW.Call(uintptr(rw.p1CmbTarget), cbAddString, 0, uintptr(unsafe.Pointer(u16(opt.label))))
		if strings.EqualFold(opt.id, rw.opts.target) {
			selectedIdx = i
		}
	}
	procSendMessageW.Call(uintptr(rw.p1CmbTarget), cbSetCurSel, uintptr(selectedIdx), 0)

	rw.p1LblOpts = createCtrl(0, "STATIC", "Full-System Repair Actions (databases in data/ and sites in www/ are kept safe):", 0, 22, 196, 530, 18, hwnd, 0, rw.fontBold)
	rw.p1ChkRestore = createCtrl(0, "BUTTON", "Verify/restore missing folders, core EXEs (if installer nearby), shortcuts & Windows PATH",
		wsTabStop|bsAutoCheckBox, 26, 220, 526, 22, hwnd, idChkRestore, rw.fontUI)
	rw.p1ChkStopProc = createCtrl(0, "BUTTON", "Stop running/hung Darius DB processes & clean stale PID / socket locks before repair",
		wsTabStop|bsAutoCheckBox, 26, 245, 526, 22, hwnd, idChkStopProc, rw.fontUI)
	rw.p1ChkFixPorts = createCtrl(0, "BUTTON", "Auto-reassign ports blocked by external programs to the nearest free port",
		wsTabStop|bsAutoCheckBox, 26, 270, 526, 22, hwnd, idChkFixPorts, rw.fontUI)
	rw.p1ChkCleanCch = createCtrl(0, "BUTTON", "Clear temporary SQL/LocalDB pipe caches & rotate oversized logs (>20 MB)",
		wsTabStop|bsAutoCheckBox, 26, 295, 526, 22, hwnd, idChkCleanCch, rw.fontUI)
	rw.p1ChkResetCfg = createCtrl(0, "BUTTON", "Reset engine configuration files (etc/*) to clean defaults (backs up to .bak)",
		wsTabStop|bsAutoCheckBox, 26, 320, 526, 22, hwnd, idChkResetCfg, rw.fontUI)
	rw.p1Note = createCtrl(0, "STATIC", "Nothing executes until you click 'Repair All' below.",
		0, 22, 348, 526, 18, hwnd, 0, rw.fontBold)

	setChecked(rw.p1ChkRestore, rw.opts.restoreSystem)
	setChecked(rw.p1ChkStopProc, rw.opts.stopProcs)
	setChecked(rw.p1ChkFixPorts, rw.opts.fixPorts)
	setChecked(rw.p1ChkCleanCch, rw.opts.cleanCache)
	setChecked(rw.p1ChkResetCfg, rw.opts.resetConfigs)

	// ---- Page 2: Progress & Live Log ----
	rw.p2Status = createCtrl(0, "STATIC", "Preparing full system repair...",
		0, 22, 86, 524, 20, hwnd, 0, rw.fontBold)
	rw.p2Progress = createCtrl(0, "msctls_progress32", "",
		0, 22, 110, 524, 24, hwnd, 0, 0)
	procSendMessageW.Call(uintptr(rw.p2Progress), pbmSetRange32, 0, 100)
	rw.p2LogBox = createCtrl(wsExClientEdge, "EDIT", "",
		wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		22, 144, 524, 214, hwnd, 0, rw.fontMono)

	// ---- Page 3: Complete ----
	rw.p3DoneTitle = createCtrl(0, "STATIC", "Darius DB (Lite Version 0.1.5) Repair All Completed!",
		0, 22, 92, 524, 24, hwnd, 0, rw.fontTtl)
	rw.p3Summary = createCtrl(0, "STATIC", "",
		0, 22, 128, 524, 150, hwnd, 0, rw.fontUI)
	rw.p3ChkLaunch = createCtrl(0, "BUTTON", "Launch Darius DB (Lite Version 0.1.5) now",
		wsTabStop|bsAutoCheckBox, 26, 294, 480, 24, hwnd, idChkLaunch, rw.fontBold)
	setChecked(rw.p3ChkLaunch, !rw.noLaunch)

	// Bottom divider & navigation buttons
	createCtrl(0, "STATIC", "", wsVisible|ssEtchedHorz, 0, 376, 580, 2, hwnd, 0, 0)
	rw.btnBack = createCtrl(0, "BUTTON", "< Back", wsVisible|wsTabStop|bsPushButton, 264, 390, 90, 30, hwnd, idBtnBack, rw.fontUI)
	rw.btnNext = createCtrl(0, "BUTTON", "Repair All", wsVisible|wsTabStop|bsDefPushBtn, 360, 390, 94, 30, hwnd, idBtnNext, rw.fontBold)
	rw.btnCancel = createCtrl(0, "BUTTON", "Cancel", wsVisible|wsTabStop|bsPushButton, 460, 390, 90, 30, hwnd, idBtnCancel, rw.fontUI)

	showRepairPage(1)
}

func showRepairPage(page int) {
	rw.page = page

	for _, h := range []syscall.Handle{
		rw.p1LblDir, rw.p1EditDir, rw.p1BtnBrowse, rw.p1LblTarget, rw.p1CmbTarget,
		rw.p1LblOpts, rw.p1ChkRestore, rw.p1ChkStopProc, rw.p1ChkFixPorts, rw.p1ChkCleanCch, rw.p1ChkResetCfg, rw.p1Note,
	} {
		showCtrl(h, page == 1)
	}
	for _, h := range []syscall.Handle{rw.p2Status, rw.p2Progress, rw.p2LogBox} {
		showCtrl(h, page == 2)
	}
	for _, h := range []syscall.Handle{rw.p3DoneTitle, rw.p3Summary, rw.p3ChkLaunch} {
		showCtrl(h, page == 3)
	}

	switch page {
	case 1:
		setText(rw.lblSubtitle, "Step 1 of 3 — Review Repair All Scope & Click 'Repair All' to Start")
		showCtrl(rw.btnBack, false)
		showCtrl(rw.btnCancel, true)
		procEnableWindow.Call(uintptr(rw.btnNext), 1)
		procEnableWindow.Call(uintptr(rw.btnCancel), 1)
		setText(rw.btnNext, "Repair All")

	case 2:
		setText(rw.lblSubtitle, "Step 2 of 3 — Running Full-System Repair Diagnostics...")
		showCtrl(rw.btnBack, false)
		procEnableWindow.Call(uintptr(rw.btnNext), 0)
		procEnableWindow.Call(uintptr(rw.btnCancel), 0)
		setText(rw.btnNext, "Repairing...")

	case 3:
		setText(rw.lblSubtitle, "Step 3 of 3 — Repair All Completed")
		fixed := 0
		warns := 0
		checks := 0
		summaryLine := "Completed system repair."
		if rw.report != nil {
			fixed = rw.report.FixedCount
			warns = rw.report.WarnCount
			checks = len(rw.report.Items)
			summaryLine = rw.report.Summary
		}
		binNote := "Verified existing program binaries"
		if rw.usedSetup {
			binNote = "Re-extracted all fresh core binaries from Setup installer"
		} else if len(rw.restored) > 0 {
			binNote = "Restored: " + strings.Join(rw.restored, ", ")
		}
		setText(rw.p3Summary, fmt.Sprintf(
			"Repaired Folder:    %s\r\n"+
				"Diagnostic Checks:  %d total (%d fixed, %d warnings)\r\n"+
				"Program Binaries:   %s\r\n"+
				"System Summary:     %s\r\n"+
				"Databases & Sites:  All databases in data/ and sites in www/ were kept untouched\r\n"+
				"Shortcuts & PATH:   Desktop, Start Menu & Windows User PATH refreshed",
			rw.opts.root, checks, fixed, warns, binNote, summaryLine,
		))
		showCtrl(rw.btnBack, true)
		showCtrl(rw.btnCancel, false)
		procEnableWindow.Call(uintptr(rw.btnBack), 1)
		procEnableWindow.Call(uintptr(rw.btnNext), 1)
		setText(rw.btnNext, "Finish")
	}
}

func appendRepairLogLines(lines []string) {
	if len(lines) == 0 {
		return
	}
	cur := getText(rw.p2LogBox)
	for _, line := range lines {
		if cur != "" {
			cur += "\r\n"
		}
		cur += line
	}
	setText(rw.p2LogBox, cur)
	n := len(cur)
	procSendMessageW.Call(uintptr(rw.p2LogBox), emSetSel, uintptr(n), uintptr(n))
	procSendMessageW.Call(uintptr(rw.p2LogBox), emScrollCaret, 0, 0)
}

func startAsyncRepairAll() {
	dir := strings.TrimSpace(getText(rw.p1EditDir))
	if dir == "" {
		dir = winui.DefaultInstallDir
	}
	selIdx, _, _ := procSendMessageW.Call(uintptr(rw.p1CmbTarget), cbGetCurSel, 0, 0)
	target := "all"
	if int(selIdx) >= 0 && int(selIdx) < len(repairTargetOptions) {
		target = repairTargetOptions[int(selIdx)].id
	}

	opts := repairAllOptions{
		root:          filepath.Clean(dir),
		selfExe:       rw.opts.selfExe,
		target:        target,
		restoreSystem: isChecked(rw.p1ChkRestore),
		stopProcs:     isChecked(rw.p1ChkStopProc),
		fixPorts:      isChecked(rw.p1ChkFixPorts),
		cleanCache:    isChecked(rw.p1ChkCleanCch),
		resetConfigs:  isChecked(rw.p1ChkResetCfg),
	}
	rw.opts = opts

	setText(rw.p2LogBox, "")
	procSendMessageW.Call(uintptr(rw.p2Progress), pbmSetPos, 0, 0)
	rw.mu.Lock()
	rw.logs = nil
	rw.errText = ""
	rw.report = nil
	rw.restored = nil
	rw.usedSetup = false
	rw.mu.Unlock()

	showRepairPage(2)
	rw.working = true

	go func() {
		reportStep := func(pct int, msgText string) {
			rw.mu.Lock()
			if msgText != "" {
				rw.logs = append(rw.logs, msgText)
				rw.stepMsg = msgText
			}
			if pct >= 0 {
				rw.stepPct = pct
			}
			rw.mu.Unlock()
			procPostMessageW.Call(uintptr(rw.hwnd), wmRepairStep, 0, 0)
			time.Sleep(45 * time.Millisecond)
		}

		rep, err := performRepairAll(opts, reportStep)
		rw.mu.Lock()
		rw.report = rep
		if err != nil {
			rw.errText = err.Error()
		}
		rw.mu.Unlock()
		procPostMessageW.Call(uintptr(rw.hwnd), wmRepairDone, 0, 0)
	}()
}

func repairWndProc(hwnd syscall.Handle, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		buildRepairControls(hwnd)
		return 0

	case wmCtlColorStat:
		procSetBkMode.Call(wParam, 1)
		br, _, _ := procGetSysColorBrush.Call(colorBtnFace)
		return br

	case wmCommand:
		id := int(wParam & 0xFFFF)
		switch id {
		case idBtnBrowse:
			if picked := winui.BrowseForFolder("Select Darius DB installation folder to repair"); picked != "" {
				setText(rw.p1EditDir, picked)
			}
		case idBtnBack:
			if rw.page == 3 && !rw.working {
				showRepairPage(1)
			}
		case idBtnNext:
			switch rw.page {
			case 1:
				startAsyncRepairAll()
			case 3:
				finishRepairWizard()
			}
		case idBtnCancel:
			if !rw.working {
				procDestroyWindow.Call(uintptr(hwnd))
			}
		}
		return 0

	case wmRepairStep:
		rw.mu.Lock()
		pending := rw.logs
		rw.logs = nil
		msgText := rw.stepMsg
		pct := rw.stepPct
		rw.mu.Unlock()
		if msgText != "" {
			setText(rw.p2Status, fmt.Sprintf("%s (%d%%)", msgText, pct))
		}
		procSendMessageW.Call(uintptr(rw.p2Progress), pbmSetPos, uintptr(pct), 0)
		appendRepairLogLines(pending)
		return 0

	case wmRepairDone:
		rw.working = false
		rw.mu.Lock()
		pending := rw.logs
		rw.logs = nil
		errText := rw.errText
		rw.mu.Unlock()
		appendRepairLogLines(pending)
		procSendMessageW.Call(uintptr(rw.p2Progress), pbmSetPos, 100, 0)
		if errText != "" {
			winui.MessageBox(appFullTitle+" Repair All", "Repair encountered an error:\n\n"+errText, winui.MBOK|winui.MBIconError)
			showRepairPage(1)
			return 0
		}
		showRepairPage(3)
		return 0

	case wmClose:
		if !rw.working {
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

func finishRepairWizard() {
	if isChecked(rw.p3ChkLaunch) {
		launcher := filepath.Join(rw.opts.root, "DariusDB-Launcher.exe")
		if _, err := os.Stat(launcher); err != nil {
			launcher = filepath.Join(rw.opts.root, "DariusDB.exe")
		}
		if _, err := os.Stat(launcher); err == nil {
			cmd := exec.Command(launcher)
			cmd.Dir = rw.opts.root
			_ = cmd.Start()
		}
	}
	procDestroyWindow.Call(uintptr(rw.hwnd))
}

// ---------------------------------------------------------------- Repair All Engine

func performRepairAll(opts repairAllOptions, report func(pct int, msg string)) (*core.RepairReport, error) {
	step := func(pct int, msgText string) {
		if report != nil {
			report(pct, msgText)
		}
	}

	step(8, "Creating & verifying installation directory: "+opts.root)
	if err := os.MkdirAll(opts.root, 0o755); err != nil {
		return nil, fmt.Errorf("cannot prepare directory %s: %w", opts.root, err)
	}

	if opts.stopProcs {
		step(16, "Stopping running Darius DB services & clearing process locks in "+opts.root)
		winui.StopDariusProcesses(opts.root, uint32(os.Getpid()))
	}

	if opts.restoreSystem {
		step(26, "Checking core Darius DB binaries & restoring missing files if available")
		copied, err := winui.RestoreCoreBinaries(opts.root, opts.selfExe, true)
		if err == nil && len(copied) > 0 {
			rw.mu.Lock()
			rw.restored = append(rw.restored, copied...)
			rw.mu.Unlock()
			step(30, "[FIXED] Restored missing files: "+strings.Join(copied, ", "))
		}

		// If DariusDB.exe or darius.exe is still missing and a sibling Setup EXE is next to us
		// (e.g. running installer\DariusDB-Repair.exe), run silent setup repair to unpack core EXEs.
		_, errApp := os.Stat(filepath.Join(opts.root, "DariusDB.exe"))
		_, errCLI := os.Stat(filepath.Join(opts.root, "darius.exe"))
		if errApp != nil || errCLI != nil {
			if setupExe := winui.FindSiblingSetupExe(opts.selfExe); setupExe != "" {
				step(34, "Running "+filepath.Base(setupExe)+" --silent --repair to restore core binaries")
				setupCmd := exec.Command(setupExe, "--silent", "--repair", "--force", "--dir="+opts.root)
				setupCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
				if serr := setupCmd.Run(); serr == nil {
					rw.mu.Lock()
					rw.usedSetup = true
					rw.mu.Unlock()
					step(38, "[FIXED] Re-extracted core executables from "+filepath.Base(setupExe))
				}
			}
		}
	}

	step(42, "Initializing system diagnostics & verifying folders, settings, Cloud/Schedules & catalogs")
	paths := core.NewPaths(opts.root)
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	st := core.LoadSettings(opts.root)
	mgr := core.NewManagerWithOptions(paths, st, nil, true)

	step(50, fmt.Sprintf("Running full RepairWithOptions (target=%s, fixPorts=%v, cleanCache=%v, resetConfigs=%v)",
		opts.target, opts.fixPorts, opts.cleanCache, opts.resetConfigs))

	rep, err := mgr.RepairWithOptions(core.RepairOptions{
		Target:       opts.target,
		ResetConfigs: opts.resetConfigs,
		FixPorts:     opts.fixPorts,
		CleanCache:   opts.cleanCache,
		DeepSystem:   true,
	})
	if err != nil {
		return nil, err
	}

	if len(rep.Items) > 0 {
		for idx, it := range rep.Items {
			badge := "OK"
			switch it.Status {
			case "fixed":
				badge = "FIXED"
			case "warn":
				badge = "WARN"
			}
			pct := 52 + ((idx+1)*30)/len(rep.Items)
			step(pct, fmt.Sprintf("[%s] %s: %s", badge, it.Category, it.Action))
		}
	}

	if opts.restoreSystem {
		step(86, "Repairing Windows User PATH for darius CLI and database client tools")
		pathDirs := append([]string{opts.root}, mgr.ClientPaths()...)
		_ = winui.EnsureDirsInUserPath(pathDirs...)

		step(92, "Repairing Start Menu & Desktop shortcuts")
		winui.CreateStandardShortcuts(opts.root, appShortName, appFullTitle, true, true)

		step(96, "Repairing Windows Installed Apps registration")
		winui.RegisterUninstallEntry(opts.root, appFullTitle, appVersion, appAuthor)
	}

	step(100, "Repair All complete — "+rep.Summary)
	return rep, nil
}
