//go:build windows

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
	appFullTitle = "Darius DB (Lite Version 0.1.3)"
	updaterVer   = "0.1.3"
	defaultDir   = `C:\DariusDB`
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB`
)

// Win32 UI constants
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
	wsExClientEdg = 0x00000200

	ssIcon       = 0x00000003
	ssEtchedHorz = 0x00000010

	bsPushButton = 0x00000000
	bsDefPushBtn = 0x00000001

	esMultiline   = 0x0004
	esPassword    = 0x0020
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
	wmUpdProgress  = wmUser + 20
	wmUpdCheckDone = wmUser + 21
	wmUpdApplyDone = wmUser + 22

	stmSetIcon    = 0x0170
	pbmSetRange32 = wmUser + 6
	pbmSetPos     = wmUser + 2
	emSetSel      = 0x00B1
	emReplaceSel  = 0x00C2
	emScrollCaret = 0x00B7
	colorBtnFace  = 15

	idBtnCheck   = 301
	idBtnUpdate  = 302
	idBtnBrowse  = 303
	idBtnLaunch  = 304
	idBtnClose   = 305
	idEditToken  = 306
	idBtnSaveTok = 307
	idBtnRepo    = 308
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")

	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
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
	procGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
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

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         syscall.Handle
	hInstance         syscall.Handle
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        unsafe.Pointer
	dwReserved        uint32
	flagsEx           uint32
}

type updaterState struct {
	hwnd      syscall.Handle
	hInst     syscall.Handle
	hIcon48   syscall.Handle
	hIconSm   syscall.Handle
	fontTitle syscall.Handle
	fontBold  syscall.Handle
	fontUI    syscall.Handle
	fontMono  syscall.Handle

	installDir   string
	installedVer string
	selfExe      string
	busy         bool

	lblStatus   syscall.Handle
	lblMeta     syscall.Handle
	progressBar syscall.Handle
	logBox      syscall.Handle
	editToken   syscall.Handle
	btnSaveTok  syscall.Handle
	btnCheck    syscall.Handle
	btnUpdate   syscall.Handle
	btnBrowse   syscall.Handle
	btnLaunch   syscall.Handle
	btnRepo     syscall.Handle
	btnClose    syscall.Handle

	mu            sync.Mutex
	pendingPct    int
	pendingStatus string
	pendingLog    []string
	lastInfo      *core.SystemUpdateInfo
	localPkgPath  string
	localPkgVer   string
	applyErr      string
	applyMsg      string
	shouldExit    bool
}

var up updaterState

func main() {
	mutexName, _ := windows.UTF16PtrFromString("Local\\DariusDBUpdaterMutex")
	hMutex, err := windows.CreateMutex(nil, false, mutexName)
	if err == windows.ERROR_ALREADY_EXISTS {
		if hMutex != 0 {
			windows.CloseHandle(hMutex)
		}
		winui.MessageBox(
			appShortName+" Updater",
			"Darius DB System Updater is already running.",
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

	installDir := ""
	tokenArg := ""
	directURL := ""
	localFile := ""
	silent := false
	checkOnly := false

	for _, a := range os.Args[1:] {
		low := strings.ToLower(a)
		switch {
		case low == "--version" || low == "-v":
			winui.MessageBox(appShortName+" Updater", fmt.Sprintf("Darius DB Standalone Updater v%s\nRepository: %s", updaterVer, core.CLIAppRepo), winui.MBOK|winui.MBIconInfo)
			return
		case low == "--silent" || low == "/s":
			silent = true
		case low == "--check":
			checkOnly = true
		case strings.HasPrefix(low, "--dir="):
			installDir = strings.Trim(a[6:], `"`)
		case strings.HasPrefix(low, "--token="):
			tokenArg = strings.TrimSpace(a[8:])
		case strings.HasPrefix(low, "--url="):
			directURL = strings.Trim(a[6:], `"`)
		case strings.HasPrefix(low, "--file="):
			localFile = strings.Trim(a[7:], `"`)
		}
	}

	if installDir == "" {
		installDir = detectInstallDir(selfExe)
	}
	installedVer := detectInstalledVersion(installDir)

	if tokenArg != "" {
		_ = core.LoadSettings(installDir).Update(func(s *core.Settings) {
			s.GitHubToken = tokenArg
		})
	}
	savedToken := tokenArg
	if savedToken == "" {
		savedToken = core.LoadSettings(installDir).Get().GitHubToken
	}

	if checkOnly {
		info, _ := core.CheckSystemUpdate(installedVer, savedToken)
		if info == nil || !info.FetchOK {
			os.Exit(1)
		}
		if info.HasUpdate {
			os.Exit(2)
		}
		os.Exit(0)
	}

	if silent {
		paths := core.NewPaths(installDir)
		updated := false
		if localFile != "" {
			if _, err := core.ApplySystemUpdateFromFile(paths, localFile); err != nil {
				os.Exit(1)
			}
			return
		}
		if directURL != "" {
			if _, err := core.ApplySystemUpdateFromURL(paths, directURL, savedToken); err != nil {
				os.Exit(1)
			}
			return
		}
		if pkgPath, pkgVer := findNewerLocalPackage(selfExe, installDir, installedVer); pkgPath != "" && compareVer(pkgVer, installedVer) > 0 {
			if _, err := core.ApplySystemUpdateFromFile(paths, pkgPath); err != nil {
				os.Exit(1)
			}
			updated = true
		}
		if !updated {
			info, _ := core.CheckSystemUpdate(installedVer, savedToken)
			if info != nil && info.FetchOK && info.HasUpdate {
				if _, err := core.ApplySystemUpdateWithFallback(paths, info.DirectSetupURL, info.FallbackSetupURL, savedToken); err != nil {
					os.Exit(1)
				}
			}
		}
		return
	}

	up.installDir = installDir
	up.installedVer = installedVer
	up.selfExe = selfExe
	runUpdaterGUI(savedToken, localFile, directURL)
}

func detectInstallDir(selfExe string) string {
	if h := strings.TrimSpace(os.Getenv("DARIUSDB_HOME")); h != "" {
		if abs, err := filepath.Abs(h); err == nil {
			return abs
		}
		return h
	}
	// Check HKCU Uninstall registry first so running installer\DariusDB-Updater.exe targets C:\DariusDB
	if k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE); err == nil {
		loc, _, err := k.GetStringValue("InstallLocation")
		k.Close()
		if err == nil && loc != "" && fileExists(filepath.Join(loc, "DariusDB.exe")) {
			return loc
		}
	}
	if selfExe != "" {
		dir := filepath.Dir(selfExe)
		if fileExists(filepath.Join(dir, "DariusDB.exe")) {
			return dir
		}
	}
	if fileExists(filepath.Join(defaultDir, "DariusDB.exe")) {
		return defaultDir
	}
	return defaultDir
}

var semverRe = regexp.MustCompile(`(\d+\.\d+(?:\.\d+)?)`)

func detectInstalledVersion(installDir string) string {
	// 1. Check HKCU Uninstall registry DisplayVersion (most reliable for installed app)
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
	// 2. Ask installed darius.exe version
	cli := filepath.Join(installDir, "darius.exe")
	if fileExists(cli) {
		cmd := exec.Command(cli, "version")
		cmd.Dir = installDir
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if out, err := cmd.Output(); err == nil {
			if m := semverRe.FindStringSubmatch(string(out)); len(m) > 1 {
				return m[1]
			}
		}
	}
	// 3. Fallback: inspect local version.json if present
	if b, err := os.ReadFile(filepath.Join(installDir, "version.json")); err == nil {
		text := string(b)
		if idx := strings.Index(text, `"currentVersion"`); idx >= 0 {
			if m := semverRe.FindStringSubmatch(text[idx:]); len(m) > 1 {
				return m[1]
			}
		}
	}
	if !fileExists(filepath.Join(installDir, "DariusDB.exe")) {
		return "0.0.0"
	}
	return "0.1.2"
}

func findNewerLocalPackage(selfExe, installDir, installedVer string) (string, string) {
	dirs := []string{}
	if selfExe != "" {
		selfDir := filepath.Dir(selfExe)
		dirs = append(dirs, selfDir, filepath.Join(filepath.Dir(selfDir), "dist"), filepath.Join(filepath.Dir(selfDir), "installer"))
	}
	dirs = append(dirs, filepath.Join(installDir, "installer"))

	bestPath := ""
	bestVer := installedVer
	seen := map[string]bool{}
	for _, d := range dirs {
		clean := strings.ToLower(filepath.Clean(d))
		if seen[clean] {
			continue
		}
		seen[clean] = true
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			low := strings.ToLower(name)
			if !(strings.HasPrefix(low, "dariusdb-setup-") && strings.HasSuffix(low, ".exe")) &&
				!(strings.HasPrefix(low, "dariusdb-") && strings.Contains(low, "portable") && strings.HasSuffix(low, ".zip")) {
				continue
			}
			m := semverRe.FindStringSubmatch(name)
			if len(m) < 2 {
				continue
			}
			ver := m[1]
			if compareVer(ver, bestVer) > 0 || (compareVer(ver, bestVer) == 0 && strings.HasSuffix(low, ".exe") && !strings.HasSuffix(strings.ToLower(bestPath), ".exe")) {
				bestVer = ver
				bestPath = filepath.Join(d, name)
			}
		}
	}
	if bestPath == "" {
		return "", ""
	}
	return bestPath, bestVer
}

func compareVer(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(pa) {
			va, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			vb, _ = strconv.Atoi(pb[i])
		}
		if va != vb {
			if va > vb {
				return 1
			}
			return -1
		}
	}
	return 0
}

// ---------------------------------------------------------------- Win32 GUI

func runUpdaterGUI(savedToken, initialFile, initialURL string) {
	runtime.LockOSThread()

	icc := initCommonControlsEx{dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})), dwICC: 0x00000020}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))

	hInst, _, _ := procGetModuleHandleW.Call(0)
	up.hInst = syscall.Handle(hInst)
	up.hIcon48 = createIconFromPNG(tray.DefaultIconPNG, 48)
	up.hIconSm = createIconFromPNG(tray.DefaultIconPNG, 16)
	up.fontTitle = createFont("Segoe UI", 18, 700)
	up.fontBold = createFont("Segoe UI", 13, 600)
	up.fontUI = createFont("Segoe UI", 13, 400)
	up.fontMono = createFont("Consolas", 12, 400)

	className, _ := windows.UTF16PtrFromString("DariusDBUpdaterClass")
	hBrush, _, _ := procGetSysColorBrush.Call(colorBtnFace)
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(updaterWndProc),
		hInstance:     up.hInst,
		hIcon:         up.hIcon48,
		hIconSm:       up.hIconSm,
		hbrBackground: syscall.Handle(hBrush),
		lpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	winW, winH := int32(600), int32(475)
	scrW, _, _ := procGetSystemMetrics.Call(0)
	scrH, _, _ := procGetSystemMetrics.Call(1)
	posX := (int32(scrW) - winW) / 2
	posY := (int32(scrH) - winH) / 2

	titlePtr, _ := windows.UTF16PtrFromString("Darius DB — System Version Updater (v" + updaterVer + ")")
	style := uint32(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsVisible)
	hwnd, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(titlePtr)),
		uintptr(style), uintptr(posX), uintptr(posY), uintptr(winW), uintptr(winH),
		0, 0, uintptr(up.hInst), 0,
	)
	up.hwnd = syscall.Handle(hwnd)
	if up.hIcon48 != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, 1, uintptr(up.hIcon48))
	}
	if up.hIconSm != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, 0, uintptr(up.hIconSm))
	}
	if savedToken != "" {
		setText(up.editToken, savedToken)
	}

	// Auto-run update check on open (or apply passed file/url)
	if initialFile != "" {
		startApplyFromFile(initialFile)
	} else if initialURL != "" {
		startApplyFromURL(initialURL, "")
	} else {
		startUpdateCheck()
	}

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

func updaterWndProc(hwnd syscall.Handle, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		buildUpdaterControls(hwnd)
		return 0

	case wmCtlColorStat:
		procSetBkMode.Call(wParam, 1)
		hBrush, _, _ := procGetSysColorBrush.Call(colorBtnFace)
		return hBrush

	case wmCommand:
		id := uint16(wParam & 0xFFFF)
		switch id {
		case idBtnCheck:
			if !up.busy {
				startUpdateCheck()
			}
		case idBtnSaveTok:
			if !up.busy {
				tok := strings.TrimSpace(getText(up.editToken))
				_ = core.LoadSettings(up.installDir).Update(func(s *core.Settings) {
					s.GitHubToken = tok
				})
				if tok != "" {
					appendLog("Saved GitHub Personal Access Token to settings.json.")
				} else {
					appendLog("Cleared GitHub Token from settings.json.")
				}
				startUpdateCheck()
			}
		case idBtnUpdate:
			if !up.busy {
				triggerSmartUpdate()
			}
		case idBtnBrowse:
			if !up.busy {
				if p := openUpdateFileDialog(hwnd); p != "" {
					startApplyFromFile(p)
				}
			}
		case idBtnLaunch:
			launchInstalledDariusDB(up.installDir)
		case idBtnRepo:
			_ = winui.OpenURL(core.CLIAppRepo)
		case idBtnClose:
			if !up.busy {
				procDestroyWindow.Call(uintptr(hwnd))
			}
		}
		return 0

	case wmUpdProgress:
		up.mu.Lock()
		pct := up.pendingPct
		status := up.pendingStatus
		lines := append([]string(nil), up.pendingLog...)
		up.pendingLog = nil
		up.mu.Unlock()
		if pct >= 0 {
			procSendMessageW.Call(uintptr(up.progressBar), pbmSetPos, uintptr(pct), 0)
		}
		if status != "" {
			setText(up.lblStatus, status)
		}
		for _, ln := range lines {
			appendLog(ln)
		}
		return 0

	case wmUpdCheckDone:
		up.busy = false
		setButtonsBusy(false)
		up.mu.Lock()
		info := up.lastInfo
		localPath := up.localPkgPath
		localVer := up.localPkgVer
		up.mu.Unlock()

		hasLocalUpgrade := localPath != "" && compareVer(localVer, up.installedVer) > 0
		hasRemoteUpgrade := info != nil && info.FetchOK && info.HasUpdate

		if hasRemoteUpgrade {
			setText(up.lblStatus, fmt.Sprintf("Update Available: Lite Version %s  (Installed: %s)", info.LatestVersion, up.installedVer))
			setText(up.btnUpdate, fmt.Sprintf("Update to v%s", info.LatestVersion))
			procEnableWindow.Call(uintptr(up.btnUpdate), 1)
			procSendMessageW.Call(uintptr(up.progressBar), pbmSetPos, 100, 0)
		} else if hasLocalUpgrade {
			setText(up.lblStatus, fmt.Sprintf("Local Update Ready: Lite Version %s  (Installed: %s)", localVer, up.installedVer))
			setText(up.btnUpdate, fmt.Sprintf("Upgrade to v%s", localVer))
			procEnableWindow.Call(uintptr(up.btnUpdate), 1)
			procSendMessageW.Call(uintptr(up.progressBar), pbmSetPos, 100, 0)
		} else if info != nil && info.FetchOK {
			setText(up.lblStatus, fmt.Sprintf("Up to Date — Darius DB Lite Version %s (Synced with GitHub)", up.installedVer))
			setText(up.btnUpdate, "Reinstall / Force Update")
			procEnableWindow.Call(uintptr(up.btnUpdate), 1)
			procSendMessageW.Call(uintptr(up.progressBar), pbmSetPos, 100, 0)
		} else {
			setText(up.lblStatus, fmt.Sprintf("Could not fetch GitHub release (Installed: %s) — see log below", up.installedVer))
			if localPath != "" {
				setText(up.btnUpdate, fmt.Sprintf("Install Local v%s", localVer))
				procEnableWindow.Call(uintptr(up.btnUpdate), 1)
			} else {
				setText(up.btnUpdate, "Update Now")
				procEnableWindow.Call(uintptr(up.btnUpdate), 0)
			}
			procSendMessageW.Call(uintptr(up.progressBar), pbmSetPos, 0, 0)
		}
		return 0

	case wmUpdApplyDone:
		up.busy = false
		setButtonsBusy(false)
		up.mu.Lock()
		errText := up.applyErr
		msgText := up.applyMsg
		exitNow := up.shouldExit
		up.mu.Unlock()
		if errText != "" {
			setText(up.lblStatus, "Update failed — see log below")
			appendLog("ERROR: " + errText)
			winui.MessageBox("Darius DB Updater", "Update failed:\n\n"+errText, winui.MBOK|winui.MBIconError)
			return 0
		}
		up.installedVer = detectInstalledVersion(up.installDir)
		setText(up.lblMeta, fmt.Sprintf("Install Folder: %s    |    Installed Version: Lite Version %s", up.installDir, up.installedVer))
		setText(up.lblStatus, msgText)
		procSendMessageW.Call(uintptr(up.progressBar), pbmSetPos, 100, 0)
		appendLog("DONE: " + msgText)
		if exitNow {
			procDestroyWindow.Call(uintptr(hwnd))
		} else {
			winui.MessageBox("Darius DB Updater", msgText, winui.MBOK|winui.MBIconInfo)
		}
		return 0

	case wmClose:
		if !up.busy {
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

func buildUpdaterControls(hwnd syscall.Handle) {
	iconCtl := createControl(hwnd, "STATIC", "", wsChild|wsVisible|ssIcon, 0, 18, 14, 48, 48, 0)
	if up.hIcon48 != 0 {
		procSendMessageW.Call(uintptr(iconCtl), stmSetIcon, uintptr(up.hIcon48), 0)
	}
	title := createControl(hwnd, "STATIC", "Darius DB System Version Updater", wsChild|wsVisible, 0, 76, 14, 495, 26, 0)
	setFont(title, up.fontTitle)

	up.lblMeta = createControl(hwnd, "STATIC",
		fmt.Sprintf("Install Folder: %s    |    Installed Version: Lite Version %s", up.installDir, up.installedVer),
		wsChild|wsVisible, 0, 76, 42, 495, 20, 0)
	setFont(up.lblMeta, up.fontUI)

	createControl(hwnd, "STATIC", "", wsChild|wsVisible|ssEtchedHorz, 0, 16, 72, 552, 2, 0)

	up.lblStatus = createControl(hwnd, "STATIC", "Checking GitHub and local packages for Darius DB updates...", wsChild|wsVisible, 0, 18, 84, 548, 22, 0)
	setFont(up.lblStatus, up.fontBold)

	up.progressBar = createControl(hwnd, "msctls_progress32", "", wsChild|wsVisible, 0, 18, 110, 548, 20, 0)
	procSendMessageW.Call(uintptr(up.progressBar), pbmSetRange32, 0, 100)

	up.logBox = createControl(hwnd, "EDIT", "", wsChild|wsVisible|wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly, wsExClientEdg, 18, 140, 548, 180, 0)
	setFont(up.logBox, up.fontMono)

	lblTok := createControl(hwnd, "STATIC", "GitHub Token (optional — only needed if repository is Private):", wsChild|wsVisible, 0, 18, 330, 548, 18, 0)
	setFont(lblTok, up.fontUI)

	up.editToken = createControl(hwnd, "EDIT", "", wsChild|wsVisible|wsTabStop|wsBorder|esPassword|esAutoHScroll, wsExClientEdg, 18, 350, 398, 24, idEditToken)
	setFont(up.editToken, up.fontUI)

	up.btnSaveTok = createControl(hwnd, "BUTTON", "Save Token & Retry", wsChild|wsVisible|wsTabStop|bsPushButton, 0, 424, 349, 142, 26, idBtnSaveTok)
	setFont(up.btnSaveTok, up.fontUI)

	createControl(hwnd, "STATIC", "", wsChild|wsVisible|ssEtchedHorz, 0, 16, 386, 552, 2, 0)

	up.btnCheck = createControl(hwnd, "BUTTON", "Check Now", wsChild|wsVisible|wsTabStop|bsPushButton, 0, 18, 398, 88, 30, idBtnCheck)
	setFont(up.btnCheck, up.fontUI)

	up.btnUpdate = createControl(hwnd, "BUTTON", "Update Now", wsChild|wsVisible|wsTabStop|bsDefPushBtn, 0, 114, 398, 125, 30, idBtnUpdate)
	setFont(up.btnUpdate, up.fontBold)

	up.btnBrowse = createControl(hwnd, "BUTTON", "Pick .exe/.zip...", wsChild|wsVisible|wsTabStop|bsPushButton, 0, 247, 398, 104, 30, idBtnBrowse)
	setFont(up.btnBrowse, up.fontUI)

	up.btnLaunch = createControl(hwnd, "BUTTON", "Launch App", wsChild|wsVisible|wsTabStop|bsPushButton, 0, 359, 398, 86, 30, idBtnLaunch)
	setFont(up.btnLaunch, up.fontUI)

	up.btnRepo = createControl(hwnd, "BUTTON", "GitHub", wsChild|wsVisible|wsTabStop|bsPushButton, 0, 452, 398, 54, 30, idBtnRepo)
	setFont(up.btnRepo, up.fontUI)

	up.btnClose = createControl(hwnd, "BUTTON", "Close", wsChild|wsVisible|wsTabStop|bsPushButton, 0, 512, 398, 54, 30, idBtnClose)
	setFont(up.btnClose, up.fontUI)
}

func setButtonsBusy(busy bool) {
	en := uintptr(1)
	if busy {
		en = 0
	}
	for _, h := range []syscall.Handle{up.btnCheck, up.btnUpdate, up.btnBrowse, up.btnSaveTok, up.btnClose} {
		procEnableWindow.Call(uintptr(h), en)
	}
}

func postProgress(pct int, status string, logLine string) {
	up.mu.Lock()
	if pct >= 0 {
		up.pendingPct = pct
	}
	if status != "" {
		up.pendingStatus = status
	}
	if logLine != "" {
		up.pendingLog = append(up.pendingLog, logLine)
	}
	up.mu.Unlock()
	if up.hwnd != 0 {
		procPostMessageW.Call(uintptr(up.hwnd), wmUpdProgress, 0, 0)
	}
}

func startUpdateCheck() {
	up.busy = true
	setButtonsBusy(true)
	tok := strings.TrimSpace(getText(up.editToken))
	if tok == "" {
		tok = core.LoadSettings(up.installDir).Get().GitHubToken
	}

	go func() {
		postProgress(15, "Checking installed version & local packages...", fmt.Sprintf("Installed location: %s (Lite Version %s)", up.installDir, up.installedVer))
		localPath, localVer := findNewerLocalPackage(up.selfExe, up.installDir, up.installedVer)
		if localPath != "" {
			postProgress(35, "", fmt.Sprintf("Found local package: %s (v%s)", filepath.Base(localPath), localVer))
		}

		postProgress(55, "Querying GitHub (version.json, commits, releases)...", "Connecting to https://github.com/dcquijano/Darius-DB-Lite-Version ...")
		info, _ := core.CheckSystemUpdate(up.installedVer, tok)

		up.mu.Lock()
		up.lastInfo = info
		up.localPkgPath = localPath
		up.localPkgVer = localVer
		up.mu.Unlock()

		if info != nil && info.FetchOK {
			postProgress(90, "", fmt.Sprintf("GitHub Sync OK [%s] -> Latest: Lite Version %s", info.FetchSource, info.LatestVersion))
			if info.LatestCommitSHA != "" {
				postProgress(-1, "", fmt.Sprintf("Latest commit [%s]: %s (%s)", info.LatestCommitSHA, info.LatestCommitMsg, info.LatestCommitDate))
			}
			if info.DirectSetupURL != "" {
				postProgress(-1, "", "Direct Setup URL: "+info.DirectSetupURL)
			}
		} else if info != nil {
			postProgress(0, "", "GitHub Fetch Failed: "+info.FetchError)
			if localPath != "" {
				postProgress(-1, "", fmt.Sprintf("Offline fallback ready: Click 'Upgrade to v%s' to install %s right now!", localVer, filepath.Base(localPath)))
			}
		}
		procPostMessageW.Call(uintptr(up.hwnd), wmUpdCheckDone, 0, 0)
	}()
}

func triggerSmartUpdate() {
	up.mu.Lock()
	info := up.lastInfo
	localPath := up.localPkgPath
	localVer := up.localPkgVer
	up.mu.Unlock()

	if info != nil && info.FetchOK && (info.DirectSetupURL != "" || info.FallbackSetupURL != "") {
		// If local package is at least as new as remote, prefer local instant upgrade!
		if localPath != "" && compareVer(localVer, info.LatestVersion) >= 0 {
			startApplyFromFile(localPath)
			return
		}
		startApplyFromURL(info.DirectSetupURL, info.FallbackSetupURL)
		return
	}
	if localPath != "" {
		startApplyFromFile(localPath)
		return
	}
	if p := openUpdateFileDialog(up.hwnd); p != "" {
		startApplyFromFile(p)
	}
}

func startApplyFromURL(primaryURL, fallbackURL string) {
	up.busy = true
	setButtonsBusy(true)
	tok := strings.TrimSpace(getText(up.editToken))
	if tok == "" {
		tok = core.LoadSettings(up.installDir).Get().GitHubToken
	}

	go func() {
		downloaded, err := downloadUpdateWithProgress(primaryURL, tok)
		if err != nil && fallbackURL != "" && !strings.EqualFold(fallbackURL, primaryURL) {
			postProgress(15, "Primary release URL unavailable; trying repository installer fallback...", "Fallback URL: "+fallbackURL)
			downloaded, err = downloadUpdateWithProgress(fallbackURL, tok)
		}
		if err != nil {
			finishApply("", err.Error(), false)
			return
		}
		applyLocalPackageWorker(downloaded)
	}()
}

func startApplyFromFile(filePath string) {
	up.busy = true
	setButtonsBusy(true)
	go func() {
		applyLocalPackageWorker(filePath)
	}()
}

func downloadUpdateWithProgress(rawURL, token string) (string, error) {
	stageDir := filepath.Join(os.TempDir(), "DariusDB-Update")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return "", err
	}
	ext := ".exe"
	if strings.Contains(strings.ToLower(rawURL), ".zip") {
		ext = ".zip"
	}
	dest := filepath.Join(stageDir, "DariusDB-Downloaded-Update"+ext)

	postProgress(10, "Downloading update package...", "Downloading: "+rawURL)
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Updater/"+updaterVer)
	low := strings.ToLower(rawURL)
	if strings.Contains(low, "api.github.com/repos/") {
		if strings.Contains(low, "/contents/") {
			req.Header.Set("Accept", "application/vnd.github.raw+json")
		} else {
			req.Header.Set("Accept", "application/octet-stream")
		}
	}
	if token != "" && (strings.Contains(low, "github.com") || strings.Contains(low, "githubusercontent.com")) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return "", fmt.Errorf("URL returned an HTML web page instead of a direct .exe/.zip installer")
	}

	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()

	total := resp.ContentLength
	buf := make([]byte, 64*1024)
	var received int64
	lastTick := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return "", werr
			}
			received += int64(n)
			if time.Since(lastTick) > 180*time.Millisecond {
				lastTick = time.Now()
				pct := 35
				if total > 0 {
					pct = 10 + int(float64(received)/float64(total)*55)
				}
				postProgress(pct, fmt.Sprintf("Downloading update... %.1f MB", float64(received)/(1024*1024)), "")
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if received < 64*1024 {
		return "", fmt.Errorf("downloaded file too small (%d bytes)", received)
	}
	postProgress(68, "Download complete! Verifying & installing update...", fmt.Sprintf("Downloaded %.2f MB to %s", float64(received)/(1024*1024), dest))
	return dest, nil
}

func isRunningInsideDir(exePath, targetDir string) bool {
	if exePath == "" || targetDir == "" {
		return false
	}
	a := strings.ToLower(filepath.Clean(exePath))
	b := strings.ToLower(filepath.Clean(targetDir)) + string(filepath.Separator)
	return strings.HasPrefix(a, b)
}

func applyLocalPackageWorker(pkgPath string) {
	paths := core.NewPaths(up.installDir)
	ext := strings.ToLower(filepath.Ext(pkgPath))

	// If running from INSIDE installDir (e.g. C:\DariusDB\DariusDB-Updater.exe), delegate to background
	// %TEMP%\DariusDB-Update script and exit immediately so DariusDB-Updater.exe itself can be replaced cleanly.
	if isRunningInsideDir(up.selfExe, up.installDir) {
		postProgress(80, "Launching background updater & restarting Darius DB...", "Delegating in-place upgrade to temp helper so all executables can be replaced...")
		msg, err := core.ApplySystemUpdateFromFile(paths, pkgPath)
		if err != nil {
			finishApply("", err.Error(), false)
			return
		}
		finishApply(msg, "", true)
		return
	}

	// Running from OUTSIDE installDir (e.g. installer\DariusDB-Updater.exe): install directly and report completion!
	postProgress(75, "Stopping any running Darius DB processes in "+up.installDir+"...", "Stopping running Darius DB processes while keeping data/ and www/ safe...")
	launcher := filepath.Join(up.installDir, "DariusDB-Launcher.exe")
	if fileExists(launcher) {
		stopCmd := exec.Command(launcher, "--stop-all")
		stopCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = stopCmd.Run()
	}
	winui.KillProcessesInDir(up.installDir, uint32(os.Getpid()))
	time.Sleep(500 * time.Millisecond)

	if ext == ".exe" {
		postProgress(85, "Running silent in-place upgrade...", fmt.Sprintf("Executing %s --silent --force --dir=%s", filepath.Base(pkgPath), up.installDir))
		cmd := exec.Command(pkgPath, "--silent", "--force", "--dir="+up.installDir)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if out, err := cmd.CombinedOutput(); err != nil {
			finishApply("", fmt.Sprintf("installer exited with error: %v %s", err, strings.TrimSpace(string(out))), false)
			return
		}
		newVer := detectInstalledVersion(up.installDir)
		postProgress(96, "Restarting Darius DB...", "Launching updated Darius DB ("+newVer+")...")
		time.Sleep(400 * time.Millisecond)
		launchInstalledDariusDB(up.installDir)
		finishApply(fmt.Sprintf("Successfully updated Darius DB at %s to Lite Version %s and restarted it! All data/ and www/ files were preserved.", up.installDir, newVer), "", true)
		return
	}

	// Portable .zip update via core helper (also restarts Darius DB automatically)
	msg, err := core.ApplySystemUpdateFromFile(paths, pkgPath)
	if err != nil {
		finishApply("", err.Error(), false)
		return
	}
	finishApply(msg, "", true)
}

func finishApply(okMsg, errMsg string, exitNow bool) {
	up.mu.Lock()
	up.applyMsg = okMsg
	up.applyErr = errMsg
	up.shouldExit = exitNow
	up.mu.Unlock()
	if up.hwnd != 0 {
		procPostMessageW.Call(uintptr(up.hwnd), wmUpdApplyDone, 0, 0)
	}
}

func launchInstalledDariusDB(installDir string) {
	exe := filepath.Join(installDir, "DariusDB-Launcher.exe")
	if !fileExists(exe) {
		exe = filepath.Join(installDir, "DariusDB.exe")
	}
	if !fileExists(exe) {
		winui.MessageBox("Darius DB Updater", "Could not find DariusDB.exe in "+installDir, winui.MBOK|winui.MBIconWarning)
		return
	}
	cmd := exec.Command(exe)
	cmd.Dir = installDir
	_ = cmd.Start()
}

func openUpdateFileDialog(owner syscall.Handle) string {
	filterStr := "Darius DB Updates (*.exe;*.zip)\x00*.exe;*.zip\x00All Files (*.*)\x00*.*\x00\x00"
	filterU16 := make([]uint16, len(filterStr))
	for i := 0; i < len(filterStr); i++ {
		filterU16[i] = uint16(filterStr[i])
	}
	fileBuf := make([]uint16, 4096)
	titlePtr, _ := windows.UTF16PtrFromString("Select DariusDB-Setup-*.exe or Portable .zip")
	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		hwndOwner:   owner,
		lpstrFilter: &filterU16[0],
		lpstrFile:   &fileBuf[0],
		nMaxFile:    uint32(len(fileBuf)),
		lpstrTitle:  titlePtr,
		flags:       0x00001000 | 0x00000800 | 0x00000008, // FILEMUSTEXIST | PATHMUSTEXIST | NOCHANGEDIR
	}
	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return ""
	}
	return windows.UTF16ToString(fileBuf)
}

func createControl(parent syscall.Handle, class, text string, style, exStyle uint32, x, y, w, h int32, id uintptr) syscall.Handle {
	cPtr, _ := windows.UTF16PtrFromString(class)
	tPtr, _ := windows.UTF16PtrFromString(text)
	hw, _, _ := procCreateWindowExW.Call(
		uintptr(exStyle), uintptr(unsafe.Pointer(cPtr)), uintptr(unsafe.Pointer(tPtr)),
		uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(parent), id, uintptr(up.hInst), 0,
	)
	return syscall.Handle(hw)
}

func createFont(face string, px, weight int32) syscall.Handle {
	fPtr, _ := windows.UTF16PtrFromString(face)
	h, _, _ := procCreateFontW.Call(
		uintptr(int32(-px)), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1, 0, 0, 5, 0, uintptr(unsafe.Pointer(fPtr)),
	)
	return syscall.Handle(h)
}

func createIconFromPNG(pngBytes []byte, size int32) syscall.Handle {
	if len(pngBytes) == 0 {
		return 0
	}
	h, _, _ := procCreateIconFromResEx.Call(
		uintptr(unsafe.Pointer(&pngBytes[0])),
		uintptr(len(pngBytes)), 1, 0x00030000, uintptr(size), uintptr(size), 0,
	)
	return syscall.Handle(h)
}

func setFont(h, f syscall.Handle) {
	procSendMessageW.Call(uintptr(h), wmSetFont, uintptr(f), 1)
}

func setText(h syscall.Handle, s string) {
	p, _ := windows.UTF16PtrFromString(s)
	procSetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(p)))
}

func getText(h syscall.Handle) string {
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(h))
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}

func appendLog(line string) {
	if up.logBox == 0 {
		return
	}
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(up.logBox))
	procSendMessageW.Call(uintptr(up.logBox), emSetSel, n, n)
	p, _ := windows.UTF16PtrFromString(line + "\r\n")
	procSendMessageW.Call(uintptr(up.logBox), emReplaceSel, 0, uintptr(unsafe.Pointer(p)))
	procSendMessageW.Call(uintptr(up.logBox), emScrollCaret, 0, 0)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
