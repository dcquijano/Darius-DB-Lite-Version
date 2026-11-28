//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
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

	"dariusdb/internal/core"
	"dariusdb/internal/tray"
	"dariusdb/internal/winui"
)

const (
	appShortName = "Darius DB"
	appFullTitle = "Darius DB (Lite Version 0.1.6)"
	appVersion   = "0.1.6"
	appAuthor    = "Darius Cumpio"
	defaultDir   = winui.DefaultInstallDir
	uninstallKey = winui.UninstallRegKey
	runKey       = winui.RunRegKey
	trailerMagic = "DARIUS01"

	mitLicenseText = "MIT License\r\n\r\n" +
		"Copyright (c) 2026 Darius Cumpio\r\n\r\n" +
		"Permission is hereby granted, free of charge, to any person obtaining a copy " +
		"of this software and associated documentation files (the \"Software\"), to deal " +
		"in the Software without restriction, including without limitation the rights " +
		"to use, copy, modify, merge, publish, distribute, sublicense, and/or sell " +
		"copies of the Software, and to permit persons to whom the Software is " +
		"furnished to do so, subject to the following conditions:\r\n\r\n" +
		"The above copyright notice and this permission notice shall be included in all " +
		"copies or substantial portions of the Software.\r\n\r\n" +
		"THE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR " +
		"IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, " +
		"FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT."
)

func main() {
	// 1. Prevent launching multiple instances of the Setup Wizard simultaneously
	mutexName, _ := windows.UTF16PtrFromString("Local\\DariusDBSetupWizardMutex")
	hMutex, err := windows.CreateMutex(nil, false, mutexName)
	if err == windows.ERROR_ALREADY_EXISTS {
		if hMutex != 0 {
			windows.CloseHandle(hMutex)
		}
		winui.MessageBox(
			appFullTitle+" Setup",
			"The Darius DB Setup Wizard is already running.",
			winui.MBOK|winui.MBIconInfo,
		)
		return
	}
	if hMutex != 0 {
		defer windows.CloseHandle(hMutex)
	}

	installDir := defaultDir
	silent := false
	forceUpdate := false
	repairMode := false
	resetConfigs := false
	fixPorts := true
	autoStart := false
	noDesktop := false
	offlineDir := ""
	offlineOnly := false
	installMissingPrereqs := false
	var cliPrereqs []string
	var cliEngines []string

	for _, a := range os.Args[1:] {
		low := strings.ToLower(strings.TrimSpace(a))
		switch {
		case low == "--silent" || low == "/s" || low == "/verysilent":
			silent = true
		case low == "--force" || low == "--update":
			forceUpdate = true
		case low == "--repair" || low == "/repair":
			repairMode = true
		case low == "--reset-config" || low == "--reset":
			resetConfigs = true
		case low == "--no-fix-ports":
			fixPorts = false
		case low == "--fix-ports":
			fixPorts = true
		case low == "--autostart":
			autoStart = true
		case low == "--no-desktop":
			noDesktop = true
		case low == "--offline-only":
			offlineOnly = true
		case low == "--install-missing-prereqs":
			installMissingPrereqs = true
		case strings.HasPrefix(low, "--dir="):
			installDir = strings.Trim(a[6:], `"`)
		case strings.HasPrefix(low, "--offline-dir="):
			offlineDir = strings.Trim(a[14:], `"`)
		case strings.HasPrefix(low, "--prereqs="):
			cliPrereqs = parsePrereqSelectionArg(a[10:])
		case strings.HasPrefix(low, "--engines="):
			cliEngines = parseEngineSelectionArg(a[10:])
		}
	}

	// 2. Check if Darius DB is already installed on this computer to prevent double installation (or offer 1-click upgrade!)
	if !forceUpdate {
		if existingDir := detectExistingInstallation(installDir); existingDir != "" {
			installedVer := detectInstalledVersion(existingDir)
			isUpgrade := installedVer != "" && installedVer != appVersion
			if silent {
				if !repairMode && !isUpgrade {
					fmt.Fprintf(os.Stderr, "Darius DB is already installed in %s. Uninstall first or pass --repair / --force.\n", existingDir)
					os.Exit(1)
				}
				installDir = existingDir
			} else if repairMode {
				wiz.repairMode = true
				wiz.finalDir = existingDir
			} else if isUpgrade {
				ans := winui.MessageBox(
					fmt.Sprintf("%s Setup — Upgrade from v%s", appFullTitle, installedVer),
					fmt.Sprintf(
						"Darius DB (Lite Version %s) is currently installed at:\n%s\n\n"+
							"A newer installer (%s) is ready!\n\n"+
							"• Click YES to UPGRADE in-place to v%s (updates DariusDB.exe, Launcher, Uninstaller, Updater & CLI while keeping all your databases in data/ and websites in www/ untouched)\n"+
							"• Click NO to open the existing Darius DB application now\n"+
							"• Click CANCEL to exit Setup",
						installedVer, existingDir, appFullTitle, appVersion,
					),
					winui.MBYesNoCancel|winui.MBIconInfo,
				)
				if ans == winui.IDYES {
					wiz.repairMode = true
					wiz.finalDir = existingDir
				} else {
					if ans == winui.IDNO {
						launcher := filepath.Join(existingDir, "DariusDB-Launcher.exe")
						if !fileExists(launcher) {
							launcher = filepath.Join(existingDir, "DariusDB.exe")
						}
						cmd := exec.Command(launcher)
						cmd.Dir = existingDir
						_ = cmd.Start()
					}
					return
				}
			} else {
				ans := winui.MessageBox(
					appFullTitle+" Setup — Already Installed (Repair or Launch)",
					fmt.Sprintf(
						"%s is already installed on your computer!\n\nInstalled location:\n%s\n\nDouble installation into a second folder is disabled.\n\n"+
							"• Click YES to REPAIR the existing installation at %s (restores missing/corrupted binaries, fixes configs, stale locks, shortcuts & PATH while keeping all your databases in data/)\n"+
							"• Click NO to open the installed Darius DB application now\n"+
							"• Click CANCEL to exit Setup",
						appFullTitle, existingDir, existingDir,
					),
					winui.MBYesNoCancel|winui.MBIconInfo,
				)
				if ans == winui.IDYES {
					wiz.repairMode = true
					wiz.finalDir = existingDir
				} else {
					if ans == winui.IDNO {
						launcher := filepath.Join(existingDir, "DariusDB-Launcher.exe")
						if !fileExists(launcher) {
							launcher = filepath.Join(existingDir, "DariusDB.exe")
						}
						cmd := exec.Command(launcher)
						cmd.Dir = existingDir
						_ = cmd.Start()
					}
					return
				}
			}
		}
	}

	if silent {
		if installMissingPrereqs && len(cliPrereqs) == 0 {
			for _, pr := range core.CheckAllPrerequisites() {
				if !pr.Installed {
					cliPrereqs = append(cliPrereqs, pr.ID)
				}
			}
		}
		opts := installOptions{
			dir:          installDir,
			desktop:      !noDesktop,
			startMenu:    true,
			addToPath:    true,
			autoStart:    autoStart,
			repair:       repairMode,
			fixPorts:     fixPorts,
			resetConfigs: resetConfigs,
			prereqs:      cliPrereqs,
			engines:      cliEngines,
			offlineDir:   offlineDir,
			offlineOnly:  offlineOnly,
		}
		if _, err := performInstall(context.Background(), opts, nil, nil); err != nil {
			fmt.Fprintln(os.Stderr, "Install error:", err)
			os.Exit(1)
		}
		return
	}

	if wiz.finalDir == "" || !wiz.repairMode {
		wiz.finalDir = installDir
	}
	wiz.fixPorts = fixPorts
	wiz.resetConfigs = resetConfigs
	wiz.offlineDir = offlineDir
	wiz.offlineOnly = offlineOnly
	wiz.cliPrereqs = cliPrereqs
	wiz.cliEngines = cliEngines
	runWizardGUI()
}

type installOptions struct {
	dir          string
	desktop      bool
	startMenu    bool
	addToPath    bool
	autoStart    bool
	repair       bool
	fixPorts     bool
	resetConfigs bool
	prereqs      []string
	engines      []string
	offlineDir   string
	offlineOnly  bool
}

type installResult struct {
	installedComponents []string
	warnings            []string
}

// ---------------------------------------------------------------- Win32 Wizard GUI

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
	wsDisabled    = 0x08000000

	wsExClientEdge = 0x00000200

	ssIcon       = 0x00000003
	ssEtchedHorz = 0x00000010

	bsPushButton   = 0x00000000
	bsDefPushBtn   = 0x00000001
	bsAutoCheckBox = 0x00000003

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
	wmInstStep     = wmUser + 10
	wmInstProg     = wmUser + 11
	wmInstDone     = wmUser + 12
	wmEnvReady     = wmUser + 13

	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1
	bstChecked = 1

	stmSetIcon = 0x0170

	pbmSetRange32 = wmUser + 6
	pbmSetPos     = wmUser + 2

	emSetSel      = 0x00B1
	emReplaceSel  = 0x00C2
	emScrollCaret = 0x00B7

	swHide = 0
	swShow = 5

	colorBtnFace = 15

	idBtnBack     = 101
	idBtnNext     = 102
	idBtnCancel   = 103
	idChkAccept   = 201
	idEditDir     = 301
	idBtnBrowse   = 302
	idChkDesktop  = 303
	idChkStartMnu = 304
	idChkPath     = 305
	idChkBoot     = 306
	idChkFixPorts = 307
	idChkResetCfg = 308

	idBtnRecheck        = 401
	idChkPrereqWebView2 = 410
	idChkPrereqVCRedist = 411
	idChkPrereqLocalDB  = 412
	idBtnPresetLite     = 420
	idBtnPresetRec      = 421
	idBtnPresetAll      = 422
	idChkEngineBase     = 430 // 430..437

	idChkLaunch = 501
	idChkGuide  = 502
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
	procInvalidateRect       = user32.NewProc("InvalidateRect")
	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procSetBkMode            = gdi32.NewProc("SetBkMode")
	procSetTextColor         = gdi32.NewProc("SetTextColor")
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
	hWnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type wizardState struct {
	hwnd         syscall.Handle
	hInst        syscall.Handle
	hIcon48      syscall.Handle
	hIconSm      syscall.Handle
	fontTitle    syscall.Handle
	fontBold     syscall.Handle
	fontUI       syscall.Handle
	fontSmall    syscall.Handle
	fontMono     syscall.Handle
	page         int // 1..5
	installing   bool
	repairMode   bool
	fixPorts     bool
	resetConfigs bool
	finalDir     string
	offlineDir   string
	offlineOnly  bool
	cliPrereqs   []string
	cliEngines   []string

	// Header
	lblTitle    syscall.Handle
	lblSubtitle syscall.Handle

	// Page 1: Welcome & License
	p1Intro  syscall.Handle
	p1LicBox syscall.Handle
	p1Accept syscall.Handle

	// Page 2: Destination & Options
	p2LblDir      syscall.Handle
	p2EditDir     syscall.Handle
	p2BtnBrw      syscall.Handle
	p2LblOpt      syscall.Handle
	p2ChkDesk     syscall.Handle
	p2ChkMenu     syscall.Handle
	p2ChkPath     syscall.Handle
	p2ChkBoot     syscall.Handle
	p2ChkFixPorts syscall.Handle
	p2ChkResetCfg syscall.Handle

	// Page 3: Requirements & Components
	p3LblPrereqs    syscall.Handle
	p3BtnRecheck    syscall.Handle
	p3ChkPrereqs    [3]syscall.Handle
	p3StPrereqs     [3]syscall.Handle
	p3DivMid        syscall.Handle
	p3LblEngines    syscall.Handle
	p3BtnPresetLite syscall.Handle
	p3BtnPresetRec  syscall.Handle
	p3BtnPresetAll  syscall.Handle
	p3ChkEngines    [8]syscall.Handle
	p3LblSummary    syscall.Handle

	// Page 4: Installing Progress
	p4Status   syscall.Handle
	p4Progress syscall.Handle
	p4LogBox   syscall.Handle

	// Page 5: Finish
	p5DoneTitle syscall.Handle
	p5Summary   syscall.Handle
	p5ChkLaunch syscall.Handle
	p5ChkGuide  syscall.Handle

	// Bottom buttons
	btnBack   syscall.Handle
	btnNext   syscall.Handle
	btnCancel syscall.Handle

	mu            sync.Mutex
	env           setupEnvSnapshot
	envLoaded     bool
	envInitDone   bool
	stepMsg       string
	stepPct       int
	logQueue      []string
	errText       string
	lastResult    installResult
}

var wiz = &wizardState{finalDir: defaultDir}

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
		uintptr(wiz.hInst),
		0,
	)
	handle := syscall.Handle(hwnd)
	if font != 0 && handle != 0 {
		procSendMessageW.Call(uintptr(handle), wmSetFont, uintptr(font), 1)
	}
	return handle
}

func showCtrl(h syscall.Handle, vis bool) {
	if h == 0 {
		return
	}
	if vis {
		procShowWindow.Call(uintptr(h), swShow)
	} else {
		procShowWindow.Call(uintptr(h), swHide)
	}
}

func setText(h syscall.Handle, s string) {
	if h == 0 {
		return
	}
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

func enableCtrl(h syscall.Handle, enabled bool) {
	if h == 0 {
		return
	}
	if enabled {
		procEnableWindow.Call(uintptr(h), 1)
	} else {
		procEnableWindow.Call(uintptr(h), 0)
	}
}

func runWizardGUI() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	icc := initCommonControlsEx{
		dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})),
		dwICC:  0x00000020 | 0x00004000, // ICC_PROGRESS_CLASS | ICC_STANDARD_CLASSES
	}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))

	hInst, _, _ := procGetModuleHandleW.Call(0)
	wiz.hInst = syscall.Handle(hInst)
	wiz.hIcon48 = makeIcon(48)
	wiz.hIconSm = makeIcon(32)
	wiz.fontTitle = makeFont(-18, 700, "Segoe UI")
	wiz.fontBold = makeFont(-13, 600, "Segoe UI")
	wiz.fontUI = makeFont(-13, 400, "Segoe UI")
	wiz.fontSmall = makeFont(-12, 400, "Segoe UI")
	wiz.fontMono = makeFont(-12, 400, "Consolas")

	clsName := u16("DariusDBSetupWizard")
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(wizardWndProc),
		hInstance:     wiz.hInst,
		hIcon:         wiz.hIcon48,
		hIconSm:       wiz.hIconSm,
		hbrBackground: syscall.Handle(colorBtnFace + 1),
		lpszClassName: clsName,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	const winW, winH = 600, 510
	scrW, _, _ := procGetSystemMetrics.Call(0)
	scrH, _, _ := procGetSystemMetrics.Call(1)
	posX := (int(scrW) - winW) / 2
	posY := (int(scrH) - winH) / 2

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(u16(appFullTitle+" Setup Wizard"))),
		wsOverlapped|wsCaption|wsSysMenu|wsMinimizeBox|wsVisible,
		uintptr(posX), uintptr(posY), winW, winH,
		0, 0, uintptr(wiz.hInst), 0,
	)
	wiz.hwnd = syscall.Handle(hwnd)
	procSendMessageW.Call(hwnd, wmSetIcon, 1, uintptr(wiz.hIcon48))
	procSendMessageW.Call(hwnd, wmSetIcon, 0, uintptr(wiz.hIconSm))

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

func buildWizardControls(hwnd syscall.Handle) {
	// Top banner icon + title + subtitle
	icoCtrl := createCtrl(0, "STATIC", "", wsVisible|ssIcon, 18, 12, 48, 48, hwnd, 0, 0)
	if wiz.hIcon48 != 0 {
		procSendMessageW.Call(uintptr(icoCtrl), stmSetIcon, uintptr(wiz.hIcon48), 0)
	}
	wiz.lblTitle = createCtrl(0, "STATIC", appFullTitle+" Setup", wsVisible, 78, 14, 495, 24, hwnd, 0, wiz.fontTitle)
	wiz.lblSubtitle = createCtrl(0, "STATIC", "Created by Darius Cumpio · MIT Open Source", wsVisible, 78, 40, 495, 20, hwnd, 0, wiz.fontUI)
	createCtrl(0, "STATIC", "", wsVisible|ssEtchedHorz, 0, 70, 600, 2, hwnd, 0, 0)

	// ---- Page 1: Welcome & License ----
	wiz.p1Intro = createCtrl(0, "STATIC",
		"Welcome to the Darius DB (Lite Version 0.1.6) Setup Wizard.\r\n"+
			"Manage Apache, PHP, MariaDB, MySQL, PostgreSQL, MS SQL Server, Redis & Mailpit with Developer CLI.",
		0, 22, 84, 540, 38, hwnd, 0, wiz.fontUI)
	wiz.p1LicBox = createCtrl(wsExClientEdge, "EDIT", mitLicenseText,
		wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		22, 128, 540, 236, hwnd, 0, wiz.fontMono)
	wiz.p1Accept = createCtrl(0, "BUTTON", "I accept the terms in the MIT License Agreement",
		wsTabStop|bsAutoCheckBox, 22, 376, 500, 22, hwnd, idChkAccept, wiz.fontBold)
	setChecked(wiz.p1Accept, true)

	// ---- Page 2: Destination & Options ----
	dirLabel := "Destination Folder (where Darius DB will be installed):"
	if wiz.repairMode {
		dirLabel = "Existing Installation Folder (locked for Repair — double install disabled):"
	}
	wiz.p2LblDir = createCtrl(0, "STATIC", dirLabel,
		0, 22, 88, 520, 20, hwnd, 0, wiz.fontBold)
	wiz.p2EditDir = createCtrl(wsExClientEdge, "EDIT", wiz.finalDir,
		wsTabStop|wsBorder|esAutoHScroll, 22, 114, 434, 26, hwnd, idEditDir, wiz.fontUI)
	wiz.p2BtnBrw = createCtrl(0, "BUTTON", "Browse...",
		wsTabStop|bsPushButton, 464, 113, 98, 28, hwnd, idBtnBrowse, wiz.fontUI)
	if wiz.repairMode {
		enableCtrl(wiz.p2EditDir, false)
		enableCtrl(wiz.p2BtnBrw, false)
	}

	wiz.p2LblOpt = createCtrl(0, "STATIC", "Shortcuts & Developer Integration:",
		0, 22, 156, 520, 18, hwnd, 0, wiz.fontBold)
	wiz.p2ChkDesk = createCtrl(0, "BUTTON", "Create / Repair Desktop shortcut",
		wsTabStop|bsAutoCheckBox, 26, 182, 520, 22, hwnd, idChkDesktop, wiz.fontUI)
	wiz.p2ChkMenu = createCtrl(0, "BUTTON", "Create / Repair Start Menu shortcuts (Darius DB, Terminal, Repair All, Uninstall)",
		wsTabStop|bsAutoCheckBox, 26, 208, 535, 22, hwnd, idChkStartMnu, wiz.fontUI)
	wiz.p2ChkPath = createCtrl(0, "BUTTON", "Add 'darius' CLI & DB tools to Windows PATH (Visual Studio & VS Code)",
		wsTabStop|bsAutoCheckBox, 26, 234, 535, 22, hwnd, idChkPath, wiz.fontUI)
	wiz.p2ChkBoot = createCtrl(0, "BUTTON", "Start Darius DB automatically when Windows signs in (minimized to tray)",
		wsTabStop|bsAutoCheckBox, 26, 260, 535, 22, hwnd, idChkBoot, wiz.fontUI)
	wiz.p2ChkFixPorts = createCtrl(0, "BUTTON", "Repair All: Auto-reassign ports blocked by external programs & run deep LocalDB fix",
		wsTabStop|bsAutoCheckBox, 26, 294, 535, 22, hwnd, idChkFixPorts, wiz.fontUI)
	wiz.p2ChkResetCfg = createCtrl(0, "BUTTON", "Reset engine configuration files (etc/*) to clean defaults (backs up to .bak)",
		wsTabStop|bsAutoCheckBox, 26, 320, 535, 22, hwnd, idChkResetCfg, wiz.fontUI)
	setChecked(wiz.p2ChkDesk, true)
	setChecked(wiz.p2ChkMenu, true)
	setChecked(wiz.p2ChkPath, true)
	setChecked(wiz.p2ChkFixPorts, wiz.fixPorts)
	setChecked(wiz.p2ChkResetCfg, wiz.resetConfigs)

	// ---- Page 3: Requirements & Components ----
	wiz.p3LblPrereqs = createCtrl(0, "STATIC", "System Requirements (auto-detected on this PC):",
		0, 22, 82, 430, 20, hwnd, 0, wiz.fontBold)
	wiz.p3BtnRecheck = createCtrl(0, "BUTTON", "Re-check",
		wsTabStop|bsPushButton, 474, 79, 88, 24, hwnd, idBtnRecheck, wiz.fontSmall)

	prereqLabels := [3]string{
		"Microsoft Edge WebView2 Runtime",
		"Visual C++ 2015-2026 Redistributable (x64)",
		"SQL Server Express LocalDB",
	}
	prereqIDs := [3]uintptr{idChkPrereqWebView2, idChkPrereqVCRedist, idChkPrereqLocalDB}
	for i := 0; i < 3; i++ {
		y := 108 + i*24
		wiz.p3ChkPrereqs[i] = createCtrl(0, "BUTTON", prereqLabels[i],
			wsTabStop|bsAutoCheckBox, 26, y, 298, 20, hwnd, prereqIDs[i], wiz.fontUI)
		wiz.p3StPrereqs[i] = createCtrl(0, "STATIC", "Checking...",
			0, 330, y+2, 232, 18, hwnd, 0, wiz.fontSmall)
		enableCtrl(wiz.p3ChkPrereqs[i], false)
	}

	wiz.p3DivMid = createCtrl(0, "STATIC", "", ssEtchedHorz, 22, 184, 540, 2, hwnd, 0, 0)

	wiz.p3LblEngines = createCtrl(0, "STATIC", "Optional Engines (uncheck all for fast Lite install):",
		0, 22, 194, 300, 20, hwnd, 0, wiz.fontBold)
	wiz.p3BtnPresetLite = createCtrl(0, "BUTTON", "Lite (None)",
		wsTabStop|bsPushButton, 326, 191, 74, 23, hwnd, idBtnPresetLite, wiz.fontSmall)
	wiz.p3BtnPresetRec = createCtrl(0, "BUTTON", "Web + DB",
		wsTabStop|bsPushButton, 406, 191, 76, 23, hwnd, idBtnPresetRec, wiz.fontSmall)
	wiz.p3BtnPresetAll = createCtrl(0, "BUTTON", "Select All",
		wsTabStop|bsPushButton, 488, 191, 74, 23, hwnd, idBtnPresetAll, wiz.fontSmall)

	for i, meta := range setupEngineOrder {
		col := i / 4
		row := i % 4
		x := 26 + col*270
		y := 222 + row*24
		wiz.p3ChkEngines[i] = createCtrl(0, "BUTTON", fmt.Sprintf("%s (%s)", meta.label, meta.hint),
			wsTabStop|bsAutoCheckBox, x, y, 264, 20, hwnd, uintptr(idChkEngineBase+i), wiz.fontUI)
	}

	wiz.p3LblSummary = createCtrl(wsExClientEdge, "EDIT",
		"Detecting installed Windows requirements and local offline packages...",
		wsBorder|esMultiline|esReadOnly,
		22, 326, 540, 76, hwnd, 0, wiz.fontSmall)

	// ---- Page 4: Installing... ----
	wiz.p4Status = createCtrl(0, "STATIC", "Preparing installation...",
		0, 22, 86, 540, 20, hwnd, 0, wiz.fontBold)
	wiz.p4Progress = createCtrl(0, "msctls_progress32", "",
		0, 22, 112, 540, 24, hwnd, 0, 0)
	procSendMessageW.Call(uintptr(wiz.p4Progress), pbmSetRange32, 0, 100)
	wiz.p4LogBox = createCtrl(wsExClientEdge, "EDIT", "",
		wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		22, 146, 540, 256, hwnd, 0, wiz.fontMono)

	// ---- Page 5: Completed ----
	doneHeadline := "Darius DB (Lite Version 0.1.6) has been installed!"
	if wiz.repairMode {
		doneHeadline = "Darius DB (Lite Version 0.1.6) has been repaired!"
	}
	wiz.p5DoneTitle = createCtrl(0, "STATIC", doneHeadline,
		0, 22, 90, 540, 26, hwnd, 0, wiz.fontTitle)
	wiz.p5Summary = createCtrl(wsExClientEdge, "EDIT", "",
		wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		22, 124, 540, 184, hwnd, 0, wiz.fontUI)
	wiz.p5ChkLaunch = createCtrl(0, "BUTTON", "Launch Darius DB (Lite Version 0.1.6) now",
		wsTabStop|bsAutoCheckBox, 26, 322, 500, 24, hwnd, idChkLaunch, wiz.fontBold)
	wiz.p5ChkGuide = createCtrl(0, "BUTTON", "Open Step-by-Step User Guide (GUIDE.md)",
		wsTabStop|bsAutoCheckBox, 26, 352, 500, 24, hwnd, idChkGuide, wiz.fontUI)
	setChecked(wiz.p5ChkLaunch, true)

	// Bottom divider & navigation buttons
	createCtrl(0, "STATIC", "", wsVisible|ssEtchedHorz, 0, 414, 600, 2, hwnd, 0, 0)
	wiz.btnBack = createCtrl(0, "BUTTON", "< Back", wsVisible|wsTabStop|bsPushButton, 284, 428, 90, 30, hwnd, idBtnBack, wiz.fontUI)
	wiz.btnNext = createCtrl(0, "BUTTON", "Next >", wsVisible|wsTabStop|bsDefPushBtn, 380, 428, 96, 30, hwnd, idBtnNext, wiz.fontBold)
	wiz.btnCancel = createCtrl(0, "BUTTON", "Cancel", wsVisible|wsTabStop|bsPushButton, 482, 428, 90, 30, hwnd, idBtnCancel, wiz.fontUI)

	if wiz.repairMode {
		setText(wiz.lblTitle, appFullTitle+" Repair")
		showWizardPage(2)
	} else {
		showWizardPage(1)
	}

	// Start prerequisite & offline pack detection asynchronously right away so Page 3 opens instantly.
	startAsyncEnvProbe(hwnd, false)
}

func startAsyncEnvProbe(hwnd syscall.Handle, forceRecheck bool) {
	dir := strings.TrimSpace(getText(wiz.p2EditDir))
	if dir == "" {
		dir = wiz.finalDir
	}
	if forceRecheck {
		enableCtrl(wiz.p3BtnRecheck, false)
		for i := 0; i < 3; i++ {
			setText(wiz.p3StPrereqs[i], "Checking...")
		}
		setText(wiz.p3LblSummary, "Re-checking Windows requirements and local offline packages...")
	}
	go func(targetDir string, recheck bool) {
		if recheck {
			core.InvalidateSystemDetectCache()
			core.InvalidateMSSQLTargetCache()
		}
		selfExe, _ := os.Executable()
		snap := probeSetupEnvironment(wiz.offlineDir, selfExe, targetDir)
		wiz.mu.Lock()
		wiz.env = snap
		wiz.envLoaded = true
		wiz.mu.Unlock()
		procPostMessageW.Call(uintptr(hwnd), wmEnvReady, 0, 0)
	}(dir, forceRecheck)
}

func applyEnvSnapshotToUI() {
	wiz.mu.Lock()
	snap := wiz.env
	initial := !wiz.envInitDone
	wiz.envInitDone = true
	wiz.mu.Unlock()

	enableCtrl(wiz.p3BtnRecheck, true)

	cliPrereqSet := map[string]bool{}
	for _, id := range wiz.cliPrereqs {
		cliPrereqSet[id] = true
	}
	cliEngineSet := map[string]bool{}
	for _, id := range wiz.cliEngines {
		cliEngineSet[id] = true
	}

	for i := 0; i < 3; i++ {
		info := snap.Prereqs[i]
		setText(wiz.p3StPrereqs[i], formatPrereqStatusBadge(info))
		procInvalidateRect.Call(uintptr(wiz.p3StPrereqs[i]), 0, 1)
		if info.Status.Installed {
			setChecked(wiz.p3ChkPrereqs[i], false)
			enableCtrl(wiz.p3ChkPrereqs[i], false)
		} else {
			enableCtrl(wiz.p3ChkPrereqs[i], true)
			if initial {
				if len(cliPrereqSet) > 0 {
					setChecked(wiz.p3ChkPrereqs[i], cliPrereqSet[info.Status.ID])
				} else {
					// Auto-check missing WebView2 (GUI requirement) and VC++ x64 (engine requirement).
					// Keep SQL LocalDB optional unless bundled offline next to Setup.
					autoCheck := info.Status.ID == core.PrereqIDWebView2 ||
						info.Status.ID == core.PrereqIDVCRedistX64 ||
						(info.Status.ID == core.PrereqIDMSSQLLocalDB && info.OfflinePath != "")
					setChecked(wiz.p3ChkPrereqs[i], autoCheck)
				}
			}
		}
	}

	for i := 0; i < len(snap.Engines) && i < len(wiz.p3ChkEngines); i++ {
		spec := snap.Engines[i]
		setText(wiz.p3ChkEngines[i], formatEngineCheckboxLabel(spec))
		if initial {
			if len(cliEngineSet) > 0 {
				setChecked(wiz.p3ChkEngines[i], cliEngineSet[spec.ID])
			} else if !wiz.repairMode && spec.OfflinePath != "" {
				// If running from an Offline Bundle Pack, pre-select bundled engines for 1-click offline install.
				setChecked(wiz.p3ChkEngines[i], true)
			} else {
				setChecked(wiz.p3ChkEngines[i], false)
			}
		}
	}

	updateComponentsSummary()
}

func updateComponentsSummary() {
	wiz.mu.Lock()
	loaded := wiz.envLoaded
	snap := wiz.env
	wiz.mu.Unlock()

	if !loaded {
		return
	}

	var selPrereqs []string
	for i := 0; i < 3; i++ {
		if !snap.Prereqs[i].Status.Installed && isChecked(wiz.p3ChkPrereqs[i]) {
			selPrereqs = append(selPrereqs, prereqDisplayLabel(snap.Prereqs[i].Status.ID))
		}
	}

	var selEngines []string
	needsVC := false
	needsLocalDB := false
	hasOnlineDownload := false
	for i := 0; i < len(snap.Engines) && i < len(wiz.p3ChkEngines); i++ {
		if isChecked(wiz.p3ChkEngines[i]) {
			spec := snap.Engines[i]
			name := spec.Label
			if spec.Version != "" {
				name += " " + spec.Version
			}
			if spec.OfflinePath != "" {
				name += " [Offline]"
			} else {
				hasOnlineDownload = true
			}
			selEngines = append(selEngines, name)
			if spec.NeedsVCRedist {
				needsVC = true
			}
			if spec.NeedsLocalDB {
				needsLocalDB = true
			}
		}
	}
	for i := 0; i < 3; i++ {
		if !snap.Prereqs[i].Status.Installed && isChecked(wiz.p3ChkPrereqs[i]) && snap.Prereqs[i].OfflinePath == "" {
			hasOnlineDownload = true
		}
	}

	var lines []string
	if snap.HasOfflinePack {
		lines = append(lines, "Offline bundle detected ("+snap.OfflineRoot+") — items marked [Offline] install with zero internet.")
	}
	if len(selPrereqs) == 0 && len(selEngines) == 0 {
		lines = append(lines, "Mode: Fast Lite / Offline Install — installs core Darius DB only (~25 MB).")
		lines = append(lines, "Built-in Apache & Mailpit work immediately; download any DB engine anytime inside Versions (Alt+4).")
	} else {
		var parts []string
		if len(selPrereqs) > 0 {
			parts = append(parts, "Requirements: "+strings.Join(selPrereqs, ", "))
		}
		if len(selEngines) > 0 {
			parts = append(parts, "Engines: "+strings.Join(selEngines, ", "))
		}
		modeNote := "Offline install (no internet required)"
		if hasOnlineDownload {
			modeNote = "Downloads selected online components during Setup"
		}
		lines = append(lines, modeNote+" — "+strings.Join(parts, " | "))
	}

	if needsVC && !snap.Prereqs[1].Status.Installed && !isChecked(wiz.p3ChkPrereqs[1]) {
		lines = append(lines, "Tip: Check Visual C++ x64 above — required to run MySQL, PostgreSQL, PHP, and Apache.")
	}
	if needsLocalDB && !snap.Prereqs[2].Status.Installed && !isChecked(wiz.p3ChkPrereqs[2]) {
		lines = append(lines, "Tip: Check SQL Server LocalDB above if you want a local MS SQL instance on this PC.")
	}
	if !snap.Prereqs[0].Status.Installed && !isChecked(wiz.p3ChkPrereqs[0]) {
		lines = append(lines, "Warning: WebView2 Runtime is missing — Darius DB desktop GUI needs WebView2 to open.")
	}

	setText(wiz.p3LblSummary, strings.Join(lines, "\r\n"))
}

func applyComponentPreset(mode string) {
	wiz.mu.Lock()
	loaded := wiz.envLoaded
	snap := wiz.env
	wiz.mu.Unlock()
	if !loaded {
		return
	}
	switch mode {
	case "lite":
		for i := 0; i < 3; i++ {
			setChecked(wiz.p3ChkPrereqs[i], false)
		}
		for i := 0; i < len(wiz.p3ChkEngines); i++ {
			setChecked(wiz.p3ChkEngines[i], false)
		}
	case "recommended":
		if !snap.Prereqs[0].Status.Installed {
			setChecked(wiz.p3ChkPrereqs[0], true)
		}
		if !snap.Prereqs[1].Status.Installed {
			setChecked(wiz.p3ChkPrereqs[1], true)
		}
		setChecked(wiz.p3ChkPrereqs[2], false)
		for i := 0; i < len(snap.Engines) && i < len(wiz.p3ChkEngines); i++ {
			setChecked(wiz.p3ChkEngines[i], snap.Engines[i].Recommended)
		}
	case "all":
		for i := 0; i < 3; i++ {
			if !snap.Prereqs[i].Status.Installed {
				setChecked(wiz.p3ChkPrereqs[i], true)
			}
		}
		for i := 0; i < len(wiz.p3ChkEngines); i++ {
			setChecked(wiz.p3ChkEngines[i], true)
		}
	}
	updateComponentsSummary()
}

func showWizardPage(page int) {
	wiz.page = page

	// Page 1 controls
	for _, h := range []syscall.Handle{wiz.p1Intro, wiz.p1LicBox, wiz.p1Accept} {
		showCtrl(h, page == 1)
	}
	// Page 2 controls
	for _, h := range []syscall.Handle{wiz.p2LblDir, wiz.p2EditDir, wiz.p2BtnBrw, wiz.p2LblOpt, wiz.p2ChkDesk, wiz.p2ChkMenu, wiz.p2ChkPath, wiz.p2ChkBoot} {
		showCtrl(h, page == 2)
	}
	showCtrl(wiz.p2ChkFixPorts, page == 2 && wiz.repairMode)
	showCtrl(wiz.p2ChkResetCfg, page == 2 && wiz.repairMode)
	// Page 3 controls
	p3Ctrls := []syscall.Handle{
		wiz.p3LblPrereqs, wiz.p3BtnRecheck,
		wiz.p3ChkPrereqs[0], wiz.p3ChkPrereqs[1], wiz.p3ChkPrereqs[2],
		wiz.p3StPrereqs[0], wiz.p3StPrereqs[1], wiz.p3StPrereqs[2],
		wiz.p3DivMid, wiz.p3LblEngines,
		wiz.p3BtnPresetLite, wiz.p3BtnPresetRec, wiz.p3BtnPresetAll,
		wiz.p3LblSummary,
	}
	for i := range wiz.p3ChkEngines {
		p3Ctrls = append(p3Ctrls, wiz.p3ChkEngines[i])
	}
	for _, h := range p3Ctrls {
		showCtrl(h, page == 3)
	}
	// Page 4 controls
	for _, h := range []syscall.Handle{wiz.p4Status, wiz.p4Progress, wiz.p4LogBox} {
		showCtrl(h, page == 4)
	}
	// Page 5 controls
	for _, h := range []syscall.Handle{wiz.p5DoneTitle, wiz.p5Summary, wiz.p5ChkLaunch, wiz.p5ChkGuide} {
		showCtrl(h, page == 5)
	}

	switch page {
	case 1:
		setText(wiz.lblSubtitle, "Step 1 of 4 — Welcome & MIT License Agreement")
		enableCtrl(wiz.btnBack, false)
		setText(wiz.btnNext, "Next >")
		enableCtrl(wiz.btnNext, isChecked(wiz.p1Accept))
	case 2:
		if wiz.repairMode {
			setText(wiz.lblSubtitle, "Maintenance Mode (Step 1 of 2) — Repair Options")
			enableCtrl(wiz.btnBack, false)
		} else {
			setText(wiz.lblSubtitle, "Step 2 of 4 — Choose Install Location & Options")
			enableCtrl(wiz.btnBack, true)
		}
		enableCtrl(wiz.btnNext, true)
		setText(wiz.btnNext, "Next >")
	case 3:
		if wiz.repairMode {
			setText(wiz.lblSubtitle, "Maintenance Mode (Step 2 of 2) — System Requirements & Components")
			setText(wiz.btnNext, "Repair All")
		} else {
			setText(wiz.lblSubtitle, "Step 3 of 4 — System Requirements & Optional Components")
			setText(wiz.btnNext, "Install")
		}
		enableCtrl(wiz.btnBack, true)
		enableCtrl(wiz.btnNext, true)
		updateComponentsSummary()
	case 4:
		if wiz.repairMode {
			setText(wiz.lblSubtitle, "Repairing Darius DB (Lite Version 0.1.6)...")
			setText(wiz.btnNext, "Repairing...")
		} else {
			setText(wiz.lblSubtitle, "Step 4 of 4 — Installing Darius DB (Lite Version 0.1.6)...")
			setText(wiz.btnNext, "Installing...")
		}
		enableCtrl(wiz.btnBack, false)
		enableCtrl(wiz.btnNext, false)
		enableCtrl(wiz.btnCancel, false)
	case 5:
		setText(wiz.lblSubtitle, "Setup Completed — Ready to Use")
		wiz.mu.Lock()
		res := wiz.lastResult
		wiz.mu.Unlock()

		var b strings.Builder
		fmt.Fprintf(&b, "Installed Folder:   %s\r\n", wiz.finalDir)
		fmt.Fprintf(&b, "Created By:         %s (November 19, 2026)\r\n", appAuthor)
		fmt.Fprintf(&b, "Developer CLI:      'darius' command ready for Visual Studio & VS Code\r\n")
		fmt.Fprintf(&b, "Repair / Uninstall: Windows Settings > Apps > Installed Apps\r\n")
		if len(res.installedComponents) > 0 {
			fmt.Fprintf(&b, "\r\nPre-installed Components:\r\n  • %s\r\n", strings.Join(res.installedComponents, "\r\n  • "))
		} else {
			fmt.Fprintf(&b, "\r\nInstall Mode:       Fast Lite (download any engine anytime in Versions)\r\n")
		}
		if len(res.warnings) > 0 {
			fmt.Fprintf(&b, "\r\nNotices (you can retry anytime inside Darius DB > Versions):\r\n  ! %s\r\n", strings.Join(res.warnings, "\r\n  ! "))
		}
		setText(wiz.p5Summary, b.String())
		showCtrl(wiz.btnBack, false)
		showCtrl(wiz.btnCancel, false)
		enableCtrl(wiz.btnNext, true)
		setText(wiz.btnNext, "Finish")
	}
}

func appendLogLine(line string) {
	cur := getText(wiz.p4LogBox)
	if cur != "" {
		cur += "\r\n"
	}
	cur += line
	setText(wiz.p4LogBox, cur)
	n := len(cur)
	procSendMessageW.Call(uintptr(wiz.p4LogBox), emSetSel, uintptr(n), uintptr(n))
	procSendMessageW.Call(uintptr(wiz.p4LogBox), emScrollCaret, 0, 0)
}

func collectSelectedPrereqsAndEngines() (prereqs []string, engines []string) {
	wiz.mu.Lock()
	snap := wiz.env
	loaded := wiz.envLoaded
	wiz.mu.Unlock()

	if loaded {
		for i := 0; i < 3; i++ {
			if !snap.Prereqs[i].Status.Installed && isChecked(wiz.p3ChkPrereqs[i]) {
				prereqs = append(prereqs, snap.Prereqs[i].Status.ID)
			}
		}
	}
	for i, meta := range setupEngineOrder {
		if isChecked(wiz.p3ChkEngines[i]) {
			engines = append(engines, meta.id)
		}
	}
	return prereqs, engines
}

func startAsyncInstall() {
	dir := strings.TrimSpace(getText(wiz.p2EditDir))
	if dir == "" {
		dir = defaultDir
	}
	// Block double-installing if not in repair mode and folder already has DariusDB.exe
	if !wiz.repairMode && fileExists(filepath.Join(dir, "DariusDB.exe")) {
		winui.MessageBox(
			appFullTitle+" Setup — Already Installed",
			fmt.Sprintf(
				"Darius DB is already installed in:\n%s\n\nPlease use Repair mode or uninstall the existing installation first to avoid double installing.",
				dir,
			),
			winui.MBOK|winui.MBIconWarning,
		)
		return
	}
	prereqs, engines := collectSelectedPrereqsAndEngines()

	wiz.finalDir = dir
	opts := installOptions{
		dir:          dir,
		desktop:      isChecked(wiz.p2ChkDesk),
		startMenu:    isChecked(wiz.p2ChkMenu),
		addToPath:    isChecked(wiz.p2ChkPath),
		autoStart:    isChecked(wiz.p2ChkBoot),
		repair:       wiz.repairMode,
		fixPorts:     isChecked(wiz.p2ChkFixPorts),
		resetConfigs: isChecked(wiz.p2ChkResetCfg),
		prereqs:      prereqs,
		engines:      engines,
		offlineDir:   wiz.offlineDir,
		offlineOnly:  wiz.offlineOnly,
	}

	setText(wiz.p4LogBox, "")
	showWizardPage(4)
	wiz.installing = true

	go func() {
		reportStep := func(pct int, msg string) {
			wiz.mu.Lock()
			wiz.stepPct = pct
			wiz.stepMsg = msg
			wiz.logQueue = append(wiz.logQueue, msg)
			wiz.mu.Unlock()
			procPostMessageW.Call(uintptr(wiz.hwnd), wmInstStep, uintptr(pct), 0)
		}
		reportStatus := func(pct int, msg string) {
			wiz.mu.Lock()
			wiz.stepPct = pct
			wiz.stepMsg = msg
			wiz.mu.Unlock()
			procPostMessageW.Call(uintptr(wiz.hwnd), wmInstProg, uintptr(pct), 0)
		}

		res, err := performInstall(context.Background(), opts, reportStep, reportStatus)
		wiz.mu.Lock()
		wiz.lastResult = res
		if err != nil {
			wiz.errText = err.Error()
		} else {
			wiz.errText = ""
		}
		wiz.mu.Unlock()
		procPostMessageW.Call(uintptr(wiz.hwnd), wmInstDone, 0, 0)
	}()
}

func wizardWndProc(hwnd syscall.Handle, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		buildWizardControls(hwnd)
		return 0

	case wmCtlColorStat:
		procSetBkMode.Call(wParam, 1) // TRANSPARENT
		ctrl := syscall.Handle(lParam)
		for i := 0; i < 3; i++ {
			if ctrl == wiz.p3StPrereqs[i] {
				wiz.mu.Lock()
				loaded := wiz.envLoaded
				installed := wiz.env.Prereqs[i].Status.Installed
				wiz.mu.Unlock()
				if loaded {
					if installed {
						procSetTextColor.Call(wParam, 0x00107C10) // RGB(16, 124, 16) green
					} else if i == 2 {
						procSetTextColor.Call(wParam, 0x000E4092) // RGB(146, 64, 14) amber (optional LocalDB)
					} else {
						procSetTextColor.Call(wParam, 0x001C1CB9) // RGB(185, 28, 28) red (missing required)
					}
				}
				break
			}
		}
		br, _, _ := procGetSysColorBrush.Call(colorBtnFace)
		return br

	case wmCommand:
		id := int(wParam & 0xFFFF)
		switch {
		case id == idChkAccept:
			if wiz.page == 1 {
				enableCtrl(wiz.btnNext, isChecked(wiz.p1Accept))
			}
		case id == idBtnBrowse:
			picked := winui.BrowseForFolder("Select destination folder for " + appFullTitle)
			if picked != "" {
				if !strings.EqualFold(filepath.Base(picked), "DariusDB") {
					picked = filepath.Join(picked, "DariusDB")
				}
				setText(wiz.p2EditDir, picked)
			}
		case id == idBtnRecheck:
			startAsyncEnvProbe(hwnd, true)
		case id == idBtnPresetLite:
			applyComponentPreset("lite")
		case id == idBtnPresetRec:
			applyComponentPreset("recommended")
		case id == idBtnPresetAll:
			applyComponentPreset("all")
		case id >= idChkPrereqWebView2 && id <= idChkPrereqLocalDB:
			updateComponentsSummary()
		case id >= idChkEngineBase && id < idChkEngineBase+len(setupEngineOrder):
			engIdx := id - idChkEngineBase
			if isChecked(wiz.p3ChkEngines[engIdx]) && setupEngineOrder[engIdx].needsVCRedist {
				wiz.mu.Lock()
				vcMissing := wiz.envLoaded && !wiz.env.Prereqs[1].Status.Installed
				wiz.mu.Unlock()
				if vcMissing {
					setChecked(wiz.p3ChkPrereqs[1], true)
				}
			}
			updateComponentsSummary()
		case id == idBtnBack:
			switch wiz.page {
			case 2:
				showWizardPage(1)
			case 3:
				showWizardPage(2)
			}
		case id == idBtnNext:
			switch wiz.page {
			case 1:
				showWizardPage(2)
			case 2:
				showWizardPage(3)
			case 3:
				startAsyncInstall()
			case 5:
				finishAndExit()
			}
		case id == idBtnCancel:
			if !wiz.installing {
				procDestroyWindow.Call(uintptr(hwnd))
			}
		}
		return 0

	case wmEnvReady:
		applyEnvSnapshotToUI()
		return 0

	case wmInstStep:
		wiz.mu.Lock()
		msgText := wiz.stepMsg
		pct := wiz.stepPct
		lines := wiz.logQueue
		wiz.logQueue = nil
		wiz.mu.Unlock()
		setText(wiz.p4Status, fmt.Sprintf("%s (%d%%)", msgText, pct))
		procSendMessageW.Call(uintptr(wiz.p4Progress), pbmSetPos, uintptr(pct), 0)
		for _, line := range lines {
			if strings.HasPrefix(line, "WARNING:") {
				appendLogLine("! " + line)
			} else {
				appendLogLine("✓ " + line)
			}
		}
		return 0

	case wmInstProg:
		wiz.mu.Lock()
		msgText := wiz.stepMsg
		pct := wiz.stepPct
		wiz.mu.Unlock()
		setText(wiz.p4Status, fmt.Sprintf("%s (%d%%)", msgText, pct))
		procSendMessageW.Call(uintptr(wiz.p4Progress), pbmSetPos, uintptr(pct), 0)
		return 0

	case wmInstDone:
		wiz.installing = false
		wiz.mu.Lock()
		errText := wiz.errText
		wiz.mu.Unlock()
		if errText != "" {
			winui.MessageBox(appFullTitle+" Setup", "Installation failed:\n\n"+errText, winui.MBOK|winui.MBIconError)
			enableCtrl(wiz.btnCancel, true)
			return 0
		}
		showWizardPage(5)
		return 0

	case wmClose:
		if !wiz.installing {
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

func finishAndExit() {
	if isChecked(wiz.p5ChkGuide) {
		guide := filepath.Join(wiz.finalDir, "GUIDE.md")
		if fileExists(guide) {
			_ = exec.Command("notepad.exe", guide).Start()
		}
	}
	if isChecked(wiz.p5ChkLaunch) {
		launcher := filepath.Join(wiz.finalDir, "DariusDB-Launcher.exe")
		if !fileExists(launcher) {
			launcher = filepath.Join(wiz.finalDir, "DariusDB.exe")
		}
		cmd := exec.Command(launcher)
		cmd.Dir = wiz.finalDir
		_ = cmd.Start()
	}
	procDestroyWindow.Call(uintptr(wiz.hwnd))
}

// ---------------------------------------------------------------- Installation Engine

func performInstall(ctx context.Context, opts installOptions, reportStep func(pct int, msg string), reportStatus func(pct int, msg string)) (installResult, error) {
	var result installResult
	step := func(pct int, msg string) {
		if reportStep != nil {
			reportStep(pct, msg)
		}
	}
	status := func(pct int, msg string) {
		if reportStatus != nil {
			reportStatus(pct, msg)
		}
	}

	hasExtra := len(opts.prereqs) > 0 || len(opts.engines) > 0
	coreMaxPct := 70
	if hasExtra {
		coreMaxPct = 34
	}

	step(5, "Creating destination folder "+opts.dir)
	if err := os.MkdirAll(opts.dir, 0o755); err != nil {
		return result, fmt.Errorf("cannot create folder %s: %w", opts.dir, err)
	}

	step(10, "Stopping any running Darius DB processes")
	winui.StopDariusProcesses(opts.dir, uint32(os.Getpid()))

	extracted, err := extractEmbeddedZip(opts.dir, 14, coreMaxPct, step)
	if err != nil {
		return result, err
	}
	if !extracted {
		if err := copyFromLocalBuild(opts.dir, 14, coreMaxPct, step); err != nil {
			return result, err
		}
	}

	dirPct := coreMaxPct + 3
	step(dirPct, "Verifying portable directories (www, bin, data, etc, logs, backups, tmp)")
	for _, sub := range []string{"www", "bin", "data", "etc", "logs", "backups", "tmp"} {
		if err := os.MkdirAll(filepath.Join(opts.dir, sub), 0o755); err != nil {
			return result, err
		}
	}

	if hasExtra {
		installed, warnings := installSelectedComponents(ctx, opts, dirPct+1, 80, step, status)
		result.installedComponents = installed
		result.warnings = warnings
	}

	cliExe := filepath.Join(opts.dir, "darius.exe")
	if fileExists(cliExe) {
		step(83, "Running Repair All diagnostics (configs, settings, ports, caches, LocalDB & stale locks)")
		repairArgs := []string{"repair", "all", "--clean-cache"}
		if opts.fixPorts {
			repairArgs = append(repairArgs, "--fix-ports")
		}
		if opts.resetConfigs {
			repairArgs = append(repairArgs, "--reset-config")
		}
		repCmd := exec.Command(cliExe, repairArgs...)
		repCmd.Dir = opts.dir
		repCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = repCmd.Run()
	}

	if opts.startMenu || opts.desktop {
		step(88, "Creating / repairing Start Menu and Desktop shortcuts")
		winui.CreateStandardShortcuts(opts.dir, appShortName, appFullTitle, opts.startMenu, opts.desktop)
	}

	step(93, "Registering in Windows Installed Apps (Add/Remove Programs)")
	winui.RegisterUninstallEntry(opts.dir, appFullTitle, appVersion, appAuthor)
	configureAutoStart(opts.dir, opts.autoStart)

	if opts.addToPath {
		step(97, "Adding 'darius' CLI & installed DB tools to Windows User PATH (Visual Studio & VS Code)")
		paths := core.NewPaths(opts.dir)
		st := core.LoadSettings(opts.dir)
		mgr := core.NewManagerWithOptions(paths, st, nil, true)
		dirs := append([]string{opts.dir}, mgr.ClientPaths()...)
		_ = winui.EnsureDirsInUserPath(dirs...)
	}

	if opts.repair {
		step(100, "Repair complete!")
	} else {
		step(100, "Installation complete!")
	}
	time.Sleep(100 * time.Millisecond)
	return result, nil
}

func extractEmbeddedZip(installDir string, startPct, endPct int, step func(int, string)) (bool, error) {
	var zr *zip.Reader
	var err error
	if data := embeddedPayloadBytes(); len(data) > 0 {
		zr, err = zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return false, fmt.Errorf("cannot read installer payload: %w", err)
		}
	} else {
		selfExe, e := os.Executable()
		if e != nil {
			return false, nil
		}
		f, e := os.Open(selfExe)
		if e != nil {
			return false, nil
		}
		defer f.Close()

		st, e := f.Stat()
		if e != nil || st.Size() < 32 {
			return false, nil
		}
		var trailer [16]byte
		if _, e := f.ReadAt(trailer[:], st.Size()-16); e != nil {
			return false, nil
		}
		if string(trailer[8:16]) != trailerMagic {
			return false, nil
		}
		zipSize := int64(binary.LittleEndian.Uint64(trailer[0:8]))
		zipOffset := st.Size() - 16 - zipSize
		if zipSize <= 0 || zipOffset < 0 {
			return false, fmt.Errorf("corrupt installer payload")
		}
		zr, err = zip.NewReader(io.NewSectionReader(f, zipOffset, zipSize), zipSize)
		if err != nil {
			return false, fmt.Errorf("cannot read installer payload: %w", err)
		}
	}

	allowed := map[string]bool{
		"DariusDB.exe":           true,
		"DariusDB-Launcher.exe":  true,
		"DariusDB-Repair.exe":    true,
		"DariusDB-Uninstall.exe": true,
		"DariusDB-Updater.exe":   true,
		"darius.exe":             true,
		"LICENSE":                true,
		"README.md":              true,
		"GUIDE.md":               true,
	}
	var matching []*zip.File
	for _, zf := range zr.File {
		if allowed[filepath.Base(zf.Name)] {
			matching = append(matching, zf)
		}
	}
	span := endPct - startPct
	if span < 1 {
		span = 1
	}
	for i, zf := range matching {
		base := filepath.Base(zf.Name)
		pct := startPct + (span*(i+1))/len(matching)
		step(pct, "Extracting "+base)
		rc, err := zf.Open()
		if err != nil {
			return false, err
		}
		dest := filepath.Join(installDir, base)
		_ = os.Remove(dest + ".old")
		out, err := os.Create(dest)
		if err != nil {
			// If executable was briefly locked, move it aside to .old and retry
			_ = os.Rename(dest, dest+".old")
			out, err = os.Create(dest)
			if err != nil {
				rc.Close()
				return false, err
			}
		}
		_, cerr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if cerr != nil {
			return false, cerr
		}
	}
	return true, nil
}

func copyFromLocalBuild(installDir string, startPct, endPct int, step func(int, string)) error {
	selfExe, _ := os.Executable()
	selfDir := filepath.Dir(selfExe)
	repoRoot := filepath.Dir(selfDir)

	findFile := func(name string) string {
		for _, c := range []string{
			filepath.Join(selfDir, name),
			filepath.Join(repoRoot, "build", "bin", name),
			filepath.Join(repoRoot, name),
		} {
			if fileExists(c) {
				return c
			}
		}
		return ""
	}

	if findFile("DariusDB.exe") == "" {
		return fmt.Errorf("DariusDB.exe was not found next to the installer or in build\\bin\\")
	}
	span := endPct - startPct
	if span < 1 {
		span = 1
	}
	total := len(winui.CoreProgramFiles)
	for i, name := range winui.CoreProgramFiles {
		src := findFile(name)
		if src == "" {
			continue
		}
		pct := startPct + (span*(i+1))/total
		step(pct, "Installing "+name)
		if err := copyFile(src, filepath.Join(installDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func configureAutoStart(installDir string, enable bool) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if !enable {
		_ = k.DeleteValue("DariusDB")
		return
	}
	mainExe := filepath.Join(installDir, "DariusDB.exe")
	_ = k.SetStringValue("DariusDB", fmt.Sprintf(`"%s" --minimized`, mainExe))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// detectExistingInstallation returns the existing install directory if Darius DB
// is already installed (via HKCU Uninstall registry key or target installDir).
func detectExistingInstallation(targetDir string) string {
	if k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE); err == nil {
		loc, _, err := k.GetStringValue("InstallLocation")
		k.Close()
		if err == nil && loc != "" && fileExists(filepath.Join(loc, "DariusDB.exe")) {
			return loc
		}
	}
	if targetDir != "" && fileExists(filepath.Join(targetDir, "DariusDB.exe")) {
		return targetDir
	}
	if fileExists(filepath.Join(defaultDir, "DariusDB.exe")) {
		return defaultDir
	}
	return ""
}

func detectInstalledVersion(installDir string) string {
	if k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE); err == nil {
		loc, _, _ := k.GetStringValue("InstallLocation")
		ver, _, errV := k.GetStringValue("DisplayVersion")
		k.Close()
		if errV == nil && strings.TrimSpace(ver) != "" {
			if loc == "" || strings.EqualFold(filepath.Clean(loc), filepath.Clean(installDir)) {
				return strings.TrimPrefix(strings.TrimSpace(ver), "v")
			}
		}
	}
	return ""
}
