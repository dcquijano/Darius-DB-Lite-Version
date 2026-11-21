//go:build windows

package winui

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// DefaultInstallDir is the default Darius DB installation folder.
	DefaultInstallDir = `C:\DariusDB`
	// UninstallRegKey is the HKCU Installed Apps registry path for Darius DB.
	UninstallRegKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB`
	// RunRegKey is the HKCU startup Run registry path.
	RunRegKey = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// CoreProgramFiles lists the Darius DB executables and documentation files
// deployed in an installation directory.
var CoreProgramFiles = []string{
	"DariusDB.exe",
	"DariusDB-Launcher.exe",
	"DariusDB-Repair.exe",
	"DariusDB-Uninstall.exe",
	"DariusDB-Updater.exe",
	"darius.exe",
	"LICENSE",
	"README.md",
	"GUIDE.md",
}

// DetectInstallDir resolves the Darius DB installation directory using:
// explicitDir -> DARIUSDB_HOME -> executable directory (if Darius DB files exist)
// -> HKCU Uninstall InstallLocation -> DefaultInstallDir.
func DetectInstallDir(explicitDir string) string {
	if clean := strings.TrimSpace(explicitDir); clean != "" {
		return filepath.Clean(clean)
	}
	if h := strings.TrimSpace(os.Getenv("DARIUSDB_HOME")); h != "" {
		if abs, err := filepath.Abs(h); err == nil {
			return abs
		}
		return filepath.Clean(h)
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		if fileExists(filepath.Join(dir, "DariusDB.exe")) || fileExists(filepath.Join(dir, "settings.json")) {
			return dir
		}
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, UninstallRegKey, registry.QUERY_VALUE); err == nil {
		loc, _, err := k.GetStringValue("InstallLocation")
		k.Close()
		if err == nil && strings.TrimSpace(loc) != "" {
			return filepath.Clean(loc)
		}
	}
	return DefaultInstallDir
}

// DetectInstalledVersion reads the installed Darius DB version from HKCU Uninstall registry.
func DetectInstalledVersion(installDir string) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, UninstallRegKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	loc, _, _ := k.GetStringValue("InstallLocation")
	ver, _, errV := k.GetStringValue("DisplayVersion")
	if errV != nil || strings.TrimSpace(ver) == "" {
		return ""
	}
	if loc == "" || strings.EqualFold(filepath.Clean(loc), filepath.Clean(installDir)) {
		return strings.TrimPrefix(strings.TrimSpace(ver), "v")
	}
	return ""
}

// CreateStandardShortcuts creates Desktop and Start Menu shortcuts for Darius DB,
// including Launcher, Terminal, Updater, Repair All, and Uninstaller.
func CreateStandardShortcuts(installDir, appShortName, appFullTitle string, createMenu, createDesktop bool) {
	mainExe := filepath.Join(installDir, "DariusDB.exe")
	launcherExe := filepath.Join(installDir, "DariusDB-Launcher.exe")
	if !fileExists(launcherExe) {
		launcherExe = mainExe
	}
	repairExe := filepath.Join(installDir, "DariusDB-Repair.exe")
	uninstExe := filepath.Join(installDir, "DariusDB-Uninstall.exe")
	updaterExe := filepath.Join(installDir, "DariusDB-Updater.exe")

	if createMenu {
		startMenu := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", appShortName)
		_ = os.MkdirAll(startMenu, 0o755)
		_ = CreateShortcut(filepath.Join(startMenu, appShortName+".lnk"), launcherExe, "", installDir, mainExe, "Launch "+appFullTitle)
		_ = CreateShortcut(filepath.Join(startMenu, appShortName+" Terminal.lnk"), launcherExe, "--terminal", installDir, mainExe, "Open "+appFullTitle+" Terminal")
		_ = CreateShortcut(filepath.Join(startMenu, "Update "+appShortName+".lnk"), updaterExe, "", installDir, mainExe, "Update "+appFullTitle)
		if fileExists(repairExe) {
			_ = CreateShortcut(filepath.Join(startMenu, "Repair All "+appShortName+".lnk"), repairExe, "", installDir, mainExe, "Repair All "+appFullTitle)
		} else {
			_ = CreateShortcut(filepath.Join(startMenu, "Repair "+appShortName+".lnk"), uninstExe, "--repair", installDir, mainExe, "Repair "+appFullTitle)
		}
		_ = CreateShortcut(filepath.Join(startMenu, "Uninstall "+appShortName+".lnk"), uninstExe, "", installDir, mainExe, "Uninstall "+appFullTitle)
	}
	if createDesktop {
		desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
		if st, err := os.Stat(desktop); err == nil && st.IsDir() {
			_ = CreateShortcut(filepath.Join(desktop, appShortName+".lnk"), launcherExe, "", installDir, mainExe, "Launch "+appFullTitle)
		}
	}
}

// RemoveStandardShortcuts deletes Start Menu and Desktop shortcuts created for Darius DB.
func RemoveStandardShortcuts(appShortName string) {
	if appData := os.Getenv("APPDATA"); appData != "" {
		_ = os.RemoveAll(filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", appShortName))
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		for _, sub := range []string{"Desktop", filepath.Join("OneDrive", "Desktop")} {
			_ = os.Remove(filepath.Join(profile, sub, appShortName+".lnk"))
		}
	}
}

// RegisterUninstallEntry writes HKCU Installed Apps metadata for Darius DB.
func RegisterUninstallEntry(installDir, appFullTitle, appVersion, appAuthor string) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, UninstallRegKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()

	mainExe := filepath.Join(installDir, "DariusDB.exe")
	uninstExe := filepath.Join(installDir, "DariusDB-Uninstall.exe")
	repairExe := filepath.Join(installDir, "DariusDB-Repair.exe")
	modifyCmd := `"` + uninstExe + `" --repair`
	if fileExists(repairExe) {
		modifyCmd = `"` + repairExe + `" --dir="` + installDir + `"`
	}

	_ = k.SetStringValue("DisplayName", appFullTitle)
	_ = k.SetStringValue("DisplayVersion", appVersion)
	_ = k.SetStringValue("Publisher", appAuthor)
	_ = k.SetStringValue("InstallLocation", installDir)
	_ = k.SetStringValue("DisplayIcon", mainExe+",0")
	_ = k.SetStringValue("InstallDate", time.Now().Format("20060102"))
	_ = k.SetDWordValue("EstimatedSize", 32000)
	_ = k.SetDWordValue("NoModify", 0)
	_ = k.SetDWordValue("NoRepair", 0)
	_ = k.SetStringValue("ModifyPath", modifyCmd)
	_ = k.SetStringValue("UninstallString", `"`+uninstExe+`"`)
	_ = k.SetStringValue("QuietUninstallString", `"`+uninstExe+`" --silent --keep-data`)
}

// StopDariusProcesses stops running Darius DB engines and executables inside root,
// skipping selfPID.
func StopDariusProcesses(root string, selfPID uint32) {
	launcher := filepath.Join(root, "DariusDB-Launcher.exe")
	if fileExists(launcher) {
		stopCmd := exec.Command(launcher, "--stop-all")
		stopCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		_ = stopCmd.Run()
	}
	KillProcessesInDir(root, selfPID)
	time.Sleep(350 * time.Millisecond)
}

// FindSiblingSetupExe looks beside selfExe (or in parent\installer / parent\dist)
// for a DariusDB-Setup-*.exe package that can restore missing binaries.
func FindSiblingSetupExe(selfExe string) string {
	if selfExe == "" {
		return ""
	}
	selfDir := filepath.Dir(selfExe)
	parentDir := filepath.Dir(selfDir)
	for _, dir := range []string{selfDir, filepath.Join(parentDir, "installer"), filepath.Join(parentDir, "dist")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			low := strings.ToLower(ent.Name())
			if strings.HasPrefix(low, "dariusdb-setup-") && strings.HasSuffix(low, ".exe") {
				return filepath.Join(dir, ent.Name())
			}
		}
	}
	return ""
}

// RestoreCoreBinaries restores missing core binaries and docs in targetDir from
// sibling files next to selfExe or build\bin\ when available. If onlyMissing is false,
// newer/existing files (except selfExe itself) are also refreshed cleanly.
func RestoreCoreBinaries(targetDir, selfExe string, onlyMissing bool) ([]string, error) {
	var restored []string
	selfClean := strings.ToLower(filepath.Clean(selfExe))
	selfDir := filepath.Dir(selfExe)
	parentDir := filepath.Dir(selfDir)

	searchDirs := []string{
		selfDir,
		filepath.Join(selfDir, "installer"),
		filepath.Join(parentDir, "build", "bin"),
		filepath.Join(parentDir, "installer"),
		parentDir,
	}

	findSource := func(name string) string {
		for _, dir := range searchDirs {
			if dir == "" {
				continue
			}
			cand := filepath.Join(dir, name)
			if fileExists(cand) && strings.ToLower(filepath.Clean(cand)) != strings.ToLower(filepath.Clean(filepath.Join(targetDir, name))) {
				return cand
			}
		}
		return ""
	}

	for _, name := range CoreProgramFiles {
		dst := filepath.Join(targetDir, name)
		if strings.ToLower(filepath.Clean(dst)) == selfClean {
			continue
		}
		if onlyMissing && fileExists(dst) {
			continue
		}
		src := findSource(name)
		if src == "" {
			continue
		}
		if err := copyFileSafe(src, dst); err != nil {
			return restored, err
		}
		restored = append(restored, name)
	}
	return restored, nil
}

// EnsureDirsInUserPath prepends any missing directories to HKCU\Environment Path
// and broadcasts WM_SETTINGCHANGE.
func EnsureDirsInUserPath(dirs ...string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, _, _ := k.GetStringValue("Path")
	updated, changed := mergeUserPath(cur, dirs, true)
	if !changed {
		return nil
	}
	if err := k.SetExpandStringValue("Path", updated); err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

// RemoveDirFromUserPath strips targetDir and any subdirectory entries under it from
// HKCU\Environment Path and broadcasts WM_SETTINGCHANGE.
func RemoveDirFromUserPath(targetDir string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	cur, _, err := k.GetStringValue("Path")
	if err != nil {
		return
	}
	updated, changed := mergeUserPath(cur, []string{targetDir}, false)
	if !changed {
		return
	}
	_ = k.SetExpandStringValue("Path", updated)
	broadcastEnvironmentChange()
}

func mergeUserPath(cur string, targets []string, add bool) (string, bool) {
	norm := func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" {
			return ""
		}
		return strings.ToLower(strings.TrimRight(filepath.Clean(p), `\/`))
	}

	targetSet := make(map[string]bool, len(targets))
	targetList := make([]string, 0, len(targets))
	for _, t := range targets {
		if nt := norm(t); nt != "" && !targetSet[nt] {
			targetSet[nt] = true
			targetList = append(targetList, filepath.Clean(t))
		}
	}

	present := make(map[string]bool)
	var kept []string
	removedAny := false
	for _, raw := range strings.Split(cur, ";") {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		np := norm(p)
		if !add {
			matched := false
			for nt := range targetSet {
				if np == nt || strings.HasPrefix(np, nt+`\`) {
					matched = true
					break
				}
			}
			if matched {
				removedAny = true
				continue
			}
		}
		if !present[np] {
			present[np] = true
			kept = append(kept, p)
		}
	}

	if !add {
		return strings.Join(kept, ";"), removedAny
	}

	var prepend []string
	for _, t := range targetList {
		nt := norm(t)
		if !present[nt] {
			present[nt] = true
			prepend = append(prepend, t)
		}
	}
	if len(prepend) == 0 {
		return cur, false
	}
	kept = append(prepend, kept...)
	return strings.Join(kept, ";"), true
}

func broadcastEnvironmentChange() {
	procSendMessageTimeoutW := user32.NewProc("SendMessageTimeoutW")
	envPtr, _ := windows.UTF16PtrFromString("Environment")
	const (
		hwndBroadcast   = 0xFFFF
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	procSendMessageTimeoutW.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(envPtr)),
		smtoAbortIfHung,
		1000,
		0,
	)
}

func copyFileSafe(src, dst string) error {
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	_ = os.Remove(dst + ".old")
	out, err := os.Create(dst)
	if err != nil {
		_ = os.Rename(dst, dst+".old")
		out, err = os.Create(dst)
		if err != nil {
			return err
		}
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
