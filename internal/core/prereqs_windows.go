//go:build windows

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"dariusdb/internal/winui"
)

const (
	PrereqIDWebView2     = "webview2"
	PrereqIDVCRedistX64  = "vcredist_x64"
	PrereqIDMSSQLLocalDB = "mssql_localdb"

	WebView2BootstrapURL     = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
	WebView2StandaloneX64URL = "https://go.microsoft.com/fwlink/?linkid=2124701"
	WebView2InfoURL          = "https://developer.microsoft.com/en-us/microsoft-edge/webview2/"

	VCRedistX64URL         = "https://aka.ms/vc14/vc_redist.x64.exe"
	VCRedistX64FallbackURL = "https://aka.ms/vs/17/release/vc_redist.x64.exe"
	VCRedistInfoURL        = "https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist"

	SQLLocalDBURL     = "https://download.microsoft.com/download/3/8/d/38de7036-2433-4207-8eae-06e247e17b25/SqlLocalDB.msi"
	SQLLocalDBInfoURL = "https://www.microsoft.com/en-us/sql-server/sql-server-downloads"
)

var vcRedistRequiredDLLs = []string{
	"vcruntime140.dll",
	"vcruntime140_1.dll",
	"msvcp140.dll",
	"msvcp140_atomic_wait.dll",
}

// PrereqStatus describes a Windows system dependency required by one or more engines.
type PrereqStatus struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Installed    bool     `json:"installed"`
	Version      string   `json:"version,omitempty"`
	Detail       string   `json:"detail"`
	MissingFiles []string `json:"missingFiles,omitempty"`
	DownloadURL  string   `json:"downloadUrl"`
	InfoURL      string   `json:"infoUrl"`
	Engines      []string `json:"engines"`
}

var (
	shell32Sys          = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = shell32Sys.NewProc("ShellExecuteExW")

	vcRedistCheckMu   sync.Mutex
	hasVCRedistX64Fn  = defaultHasVCRedistX64
)

// HasVCRedistX64 reports whether the Microsoft Visual C++ 2015-2026 (x64) runtime DLLs
// are present in Windows System32.
func HasVCRedistX64() bool {
	vcRedistCheckMu.Lock()
	fn := hasVCRedistX64Fn
	vcRedistCheckMu.Unlock()
	return fn()
}

func defaultHasVCRedistX64() bool {
	return len(missingDLLsInDir(windowsSystem32Dir(), vcRedistRequiredDLLs)) == 0
}

func windowsSystem32Dir() string {
	sysRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	return filepath.Join(sysRoot, "System32")
}

func missingDLLsInDir(dir string, dlls []string) []string {
	if strings.TrimSpace(dir) == "" {
		return append([]string(nil), dlls...)
	}
	var missing []string
	for _, dll := range dlls {
		if !fileExists(filepath.Join(dir, dll)) {
			missing = append(missing, dll)
		}
	}
	return missing
}

// dirHasVCRuntime reports whether a local bin directory bundles all required VC++ x64 DLLs
// (for example, MariaDB bundles them locally inside bin/).
func dirHasVCRuntime(dir string) bool {
	return len(missingDLLsInDir(dir, vcRedistRequiredDLLs)) == 0
}

func readVCRedistRegistryVersion() (bool, string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\VisualStudio\14.0\VC\Runtimes\x64`, registry.QUERY_VALUE)
	if err != nil {
		return false, ""
	}
	defer k.Close()
	installed, _, err := k.GetIntegerValue("Installed")
	if err != nil || installed != 1 {
		return false, ""
	}
	if ver, _, err := k.GetStringValue("Version"); err == nil && strings.TrimSpace(ver) != "" {
		return true, strings.TrimPrefix(strings.TrimSpace(ver), "v")
	}
	return true, ""
}

// CheckVCRedistX64 inspects Windows System32 and the registry for the VC++ 2015-2026 (x64) runtime.
func CheckVCRedistX64() PrereqStatus {
	sys32 := windowsSystem32Dir()
	missing := missingDLLsInDir(sys32, vcRedistRequiredDLLs)
	regOK, regVer := readVCRedistRegistryVersion()
	installed := len(missing) == 0

	detail := ""
	if installed {
		if regVer != "" {
			detail = fmt.Sprintf("Installed in System32 (v%s)", regVer)
		} else if regOK {
			detail = "Installed in System32"
		} else {
			detail = "Required VC++ runtime DLLs found in System32"
		}
	} else {
		detail = fmt.Sprintf("Missing in System32: %s — required by MySQL, PostgreSQL, PHP, and Apache (exit status 0xc0000135)", strings.Join(missing, ", "))
	}

	return PrereqStatus{
		ID:           PrereqIDVCRedistX64,
		Name:         "Microsoft Visual C++ 2015–2026 Redistributable (x64)",
		Installed:    installed,
		Version:      regVer,
		Detail:       detail,
		MissingFiles: missing,
		DownloadURL:  VCRedistX64URL,
		InfoURL:      VCRedistInfoURL,
		Engines:      []string{"mysql", "postgresql", "php", "apache"},
	}
}

// CheckSQLLocalDB inspects Windows for Microsoft SQL Server Express LocalDB (SqlLocalDB.exe).
func CheckSQLLocalDB() PrereqStatus {
	localDB := findSqlLocalDB()
	installed := localDB != ""
	ver := ""
	detail := "SqlLocalDB.exe not found on this PC — required to run a local MS SQL Server instance (or connect to a running TCP SQL Server on port 1433)"
	if installed {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, localDB, "versions")
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err == nil {
			if m := dottedVerRe.FindString(string(out)); m != "" {
				ver = m
			}
		}
		cancel()
		if ver != "" {
			detail = fmt.Sprintf("Installed (%s · v%s)", localDB, ver)
		} else {
			detail = fmt.Sprintf("Installed (%s)", localDB)
		}
	}
	return PrereqStatus{
		ID:          PrereqIDMSSQLLocalDB,
		Name:        "Microsoft SQL Server Express LocalDB",
		Installed:   installed,
		Version:     ver,
		Detail:      detail,
		DownloadURL: SQLLocalDBURL,
		InfoURL:     SQLLocalDBInfoURL,
		Engines:     []string{"mssql"},
	}
}

// CheckWebView2 inspects Windows for Microsoft Edge WebView2 Runtime (required by the Darius DB GUI).
func CheckWebView2() PrereqStatus {
	installed, ver := winui.WebView2Version()
	detail := "Microsoft Edge WebView2 Runtime not found — required to open the Darius DB desktop interface"
	if installed {
		if ver != "" {
			detail = fmt.Sprintf("Installed (v%s)", ver)
		} else {
			detail = "Installed"
		}
	}
	return PrereqStatus{
		ID:          PrereqIDWebView2,
		Name:        "Microsoft Edge WebView2 Runtime",
		Installed:   installed,
		Version:     ver,
		Detail:      detail,
		DownloadURL: WebView2BootstrapURL,
		InfoURL:     WebView2InfoURL,
		Engines:     []string{"gui"},
	}
}

// CheckPrerequisites returns the installation status of external Windows runtimes used by Darius DB engines.
func CheckPrerequisites() []PrereqStatus {
	return []PrereqStatus{
		CheckVCRedistX64(),
		CheckSQLLocalDB(),
	}
}

// CheckAllPrerequisites returns the installation status of all Windows prerequisites,
// including WebView2 Runtime, VC++ 2015-2026 x64 Redistributable, and SQL Server Express LocalDB.
func CheckAllPrerequisites() []PrereqStatus {
	return []PrereqStatus{
		CheckWebView2(),
		CheckVCRedistX64(),
		CheckSQLLocalDB(),
	}
}

func isDLLNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "0xc0000135") || strings.Contains(low, "-1073741515") || strings.Contains(low, "status_dll_not_found")
}

func missingVCRedistError(subject string) error {
	missing := missingDLLsInDir(windowsSystem32Dir(), vcRedistRequiredDLLs)
	if len(missing) == 0 {
		missing = vcRedistRequiredDLLs
	}
	return fmt.Errorf("%s failed: exit status 0xc0000135 — Missing Microsoft Visual C++ 2015–2026 Redistributable (x64) (%s). Click Auto-Install VC++ x64 or download vc_redist.x64.exe", subject, strings.Join(missing, ", "))
}

func annotateWindowsExecError(subject string, err error) error {
	if err == nil {
		return nil
	}
	if isDLLNotFoundErr(err) && !strings.Contains(strings.ToLower(err.Error()), "vc_redist") {
		return fmt.Errorf("%w — Missing Microsoft Visual C++ 2015–2026 Redistributable (x64) (VCRUNTIME140.dll / MSVCP140.dll). Click Auto-Install VC++ x64 or download vc_redist.x64.exe", err)
	}
	return err
}

func prereqInstallTarget(id string) (name string, urls []string, fileName string, err error) {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case PrereqIDWebView2:
		return "Microsoft Edge WebView2 Runtime",
			[]string{WebView2BootstrapURL, WebView2StandaloneX64URL},
			"MicrosoftEdgeWebview2Setup.exe",
			nil
	case PrereqIDVCRedistX64:
		return "Visual C++ 2015–2026 Runtime (x64)",
			[]string{VCRedistX64URL, VCRedistX64FallbackURL},
			"vc_redist.x64.exe",
			nil
	case PrereqIDMSSQLLocalDB:
		return "SQL Server Express LocalDB",
			[]string{SQLLocalDBURL},
			"SqlLocalDB.msi",
			nil
	default:
		return "", nil, "", fmt.Errorf("unknown prerequisite %q", id)
	}
}

func prereqOfflineFileNames(id string) []string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case PrereqIDWebView2:
		return []string{
			"MicrosoftEdgeWebView2RuntimeInstallerX64.exe",
			"MicrosoftEdgeWebview2Setup.exe",
			"webview2-x64.exe",
		}
	case PrereqIDVCRedistX64:
		return []string{
			"vc_redist.x64.exe",
			"VC_redist.x64.exe",
		}
	case PrereqIDMSSQLLocalDB:
		return []string{
			"SqlLocalDB.msi",
			"sqllocaldb.msi",
		}
	default:
		return nil
	}
}

// FindOfflinePrereq searches searchDirs (and their prereqs/ and offline/prereqs/ subfolders)
// for a locally staged installer matching prerequisite id.
func FindOfflinePrereq(id string, searchDirs []string) string {
	names := prereqOfflineFileNames(id)
	if len(names) == 0 {
		return ""
	}
	seen := map[string]bool{}
	tryDir := func(dir string) string {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return ""
		}
		clean := strings.ToLower(filepath.Clean(dir))
		if seen[clean] {
			return ""
		}
		seen[clean] = true
		for _, fn := range names {
			p := filepath.Join(dir, fn)
			if fileExists(p) {
				return p
			}
		}
		return ""
	}
	for _, root := range searchDirs {
		if strings.TrimSpace(root) == "" {
			continue
		}
		for _, sub := range []string{
			filepath.Join(root, "offline", "prereqs"),
			filepath.Join(root, "prereqs"),
			filepath.Join(root, "offline"),
			root,
		} {
			if found := tryDir(sub); found != "" {
				return found
			}
		}
	}
	return ""
}

// InstallPrerequisite downloads and launches the official Microsoft installer for id
// (with UAC elevation and passive progress), reporting progress through emit.
func InstallPrerequisite(ctx context.Context, p *Paths, id string, emit func(Progress)) error {
	return InstallPrerequisiteWithOffline(ctx, p, id, nil, false, emit)
}

// InstallPrerequisiteWithOffline installs prerequisite id using a locally bundled installer from
// offlineDirs when present, or falls back to downloading from Microsoft unless offlineOnly is true.
func InstallPrerequisiteWithOffline(ctx context.Context, p *Paths, id string, offlineDirs []string, offlineOnly bool, emit func(Progress)) error {
	if p == nil {
		return errors.New("paths not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.ToLower(strings.TrimSpace(id))
	displayName, urls, fileName, err := prereqInstallTarget(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Tmp, 0o755); err != nil {
		return err
	}
	report := func(phase string, received, total int64, msg string) {
		if emit != nil {
			emit(Progress{
				Engine:   "prereq",
				Version:  id,
				Phase:    phase,
				Received: received,
				Total:    total,
				Message:  msg,
			})
		}
	}

	dest := FindOfflinePrereq(id, offlineDirs)
	fromOffline := dest != ""
	if !fromOffline {
		if offlineOnly {
			err := fmt.Errorf("%s offline installer (%s) was not found in the offline bundle", displayName, fileName)
			report("error", 0, 0, err.Error())
			return err
		}
		dest = filepath.Join(p.Tmp, fileName)
		report("downloading", 0, 0, "Downloading "+displayName+"...")
		var dlErr error
		for _, u := range urls {
			if ctx.Err() != nil {
				dlErr = ctx.Err()
				break
			}
			dlErr = downloadPrereqFile(ctx, u, dest, func(rec, tot int64) {
				report("downloading", rec, tot, "")
			})
			if dlErr == nil {
				break
			}
		}
		if dlErr != nil {
			phase := "error"
			msg := dlErr.Error()
			if ctx.Err() != nil {
				phase = "cancelled"
				msg = "cancelled"
			}
			report(phase, 0, 0, msg)
			return dlErr
		}
	} else {
		report("installing", 1, 1, fmt.Sprintf("Using offline %s installer (%s)...", displayName, filepath.Base(dest)))
	}

	report("installing", 1, 1, "Running Microsoft "+displayName+" installer (approve Windows UAC prompt if shown)...")

	var exePath, params string
	switch id {
	case PrereqIDWebView2:
		exePath = dest
		params = "/silent /install"
	case PrereqIDVCRedistX64:
		exePath = dest
		params = "/install /passive /norestart"
	case PrereqIDMSSQLLocalDB:
		exePath = filepath.Join(windowsSystem32Dir(), "msiexec.exe")
		params = fmt.Sprintf(`/i "%s" /passive /norestart IACCEPTSQLLOCALDBLICENSETERMS=YES`, dest)
	}

	exitCode, runErr := runElevatedAndWait(ctx, exePath, params, p.Tmp)
	if !fromOffline {
		_ = os.Remove(dest)
	}
	if runErr != nil {
		report("error", 0, 0, runErr.Error())
		return runErr
	}

	InvalidateSystemDetectCache()

	if id == PrereqIDWebView2 {
		if winui.WebView2Installed() {
			msg := displayName + " installed!"
			if exitCode == 3010 {
				msg = displayName + " installed (Windows reboot recommended)."
			}
			report("done", 1, 1, msg)
			return nil
		}
		if exitCode != 0 && exitCode != 3010 && exitCode != 1638 {
			err := fmt.Errorf("%s installer exited with code %d — try downloading and running it manually from Microsoft", displayName, exitCode)
			report("error", 0, 0, err.Error())
			return err
		}
		err := fmt.Errorf("%s installer finished (exit code %d), but WebView2 Runtime was not detected yet", displayName, exitCode)
		report("error", 0, 0, err.Error())
		return err
	}

	// Standard Windows installer success codes:
	// 0 = success, 3010 = success (reboot recommended), 1638 = newer version already installed.
	if exitCode != 0 && exitCode != 3010 && exitCode != 1638 {
		err := fmt.Errorf("%s installer exited with code %d — try downloading and running it manually from Microsoft", displayName, exitCode)
		report("error", 0, 0, err.Error())
		return err
	}

	if id == PrereqIDVCRedistX64 {
		if !HasVCRedistX64() {
			err := fmt.Errorf("%s installer finished (exit code %d), but System32 DLLs are still missing — a Windows restart may be required", displayName, exitCode)
			report("error", 0, 0, err.Error())
			return err
		}
		msg := displayName + " installed! You can now start MySQL, PostgreSQL, PHP, and Apache."
		if exitCode == 3010 {
			msg = displayName + " installed (Windows reboot recommended if services still fail to load DLLs)."
		}
		report("done", 1, 1, msg)
		return nil
	}

	// PrereqIDMSSQLLocalDB
	localDB := findSqlLocalDB()
	if localDB == "" {
		err := fmt.Errorf("SQL Server Express LocalDB installer finished (exit code %d), but SqlLocalDB.exe was not detected yet", exitCode)
		report("error", 0, 0, err.Error())
		return err
	}
	createCtx, createCancel := context.WithTimeout(context.Background(), 20*time.Second)
	_ = runQuiet(createCtx, nil, localDB, "create", mssqlInstance)
	createCancel()
	InvalidateMSSQLTargetCache()

	msg := "SQL Server Express LocalDB installed and " + mssqlInstance + " instance verified! You can now start MS SQL Server."
	if exitCode == 3010 {
		msg = "SQL Server Express LocalDB installed (reboot recommended if LocalDB fails to start)."
	}
	report("done", 1, 1, msg)
	return nil
}

func downloadPrereqFile(ctx context.Context, rawURL, dest string, onProgress func(received, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DariusDB-Lite/0.1.6")
	resp, err := downloadHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: HTTP %s", resp.Status)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.Contains(ct, "text/html") {
		return fmt.Errorf("remote host returned an HTML page instead of an installer binary")
	}
	part := dest + ".part"
	_ = os.Remove(part)
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	total := resp.ContentLength
	var received int64
	last := time.Time{}
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				_ = os.Remove(part)
				return werr
			}
			received += int64(n)
			if onProgress != nil && time.Since(last) > 180*time.Millisecond {
				last = time.Now()
				onProgress(received, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			_ = os.Remove(part)
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(part)
		return err
	}
	if total > 0 && received != total {
		_ = os.Remove(part)
		return fmt.Errorf("incomplete download (%d of %d bytes)", received, total)
	}
	if onProgress != nil {
		onProgress(received, total)
	}
	_ = os.Remove(dest)
	return os.Rename(part, dest)
}

type shellExecuteInfoW struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     windows.Handle
}

func runElevatedAndWait(ctx context.Context, exePath, params, workDir string) (uint32, error) {
	verbPtr, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 1, err
	}
	filePtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return 1, err
	}
	var paramsPtr *uint16
	if strings.TrimSpace(params) != "" {
		paramsPtr, err = windows.UTF16PtrFromString(params)
		if err != nil {
			return 1, err
		}
	}
	var dirPtr *uint16
	if strings.TrimSpace(workDir) != "" {
		dirPtr, err = windows.UTF16PtrFromString(workDir)
		if err != nil {
			return 1, err
		}
	}

	const (
		seeMaskNoCloseProcess = 0x00000040
		seeMaskNoAsync        = 0x00000100
		swShowNormal          = 1
	)
	sei := shellExecuteInfoW{
		cbSize:       uint32(unsafe.Sizeof(shellExecuteInfoW{})),
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb:       verbPtr,
		lpFile:       filePtr,
		lpParameters: paramsPtr,
		lpDirectory:  dirPtr,
		nShow:        swShowNormal,
	}
	r1, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if r1 == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == windows.ERROR_CANCELLED {
			return 1223, errors.New("administrator approval (UAC) was cancelled — approve the Windows prompt or use the direct download link")
		}
		if callErr != nil && callErr != syscall.Errno(0) {
			return 1, fmt.Errorf("failed to launch elevated installer: %w", callErr)
		}
		return 1, errors.New("failed to launch elevated installer")
	}
	if sei.hProcess == 0 {
		return 0, nil
	}
	defer windows.CloseHandle(sei.hProcess)

	for {
		ev, werr := windows.WaitForSingleObject(sei.hProcess, 250)
		if ev == windows.WAIT_OBJECT_0 {
			break
		}
		if werr != nil && ev != uint32(windows.WAIT_TIMEOUT) {
			return 1, werr
		}
		if ctx != nil && ctx.Err() != nil {
			_ = windows.TerminateProcess(sei.hProcess, 1)
			return 1, ctx.Err()
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(sei.hProcess, &code); err != nil {
		return 1, err
	}
	return code, nil
}
