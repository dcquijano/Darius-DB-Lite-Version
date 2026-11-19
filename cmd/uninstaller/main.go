//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"

	"dariusdb/internal/winui"
)

const (
	title        = "Uninstall Darius DB (Lite Version 0.1.2)"
	appVersion   = "0.1.2"
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
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

func main() {
	root := resolveRoot()
	selfExe, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(selfExe); err == nil {
		selfExe = real
	}

	silent := false
	keepData := true
	repairOnly := false
	for _, a := range os.Args[1:] {
		switch strings.ToLower(a) {
		case "--silent", "/s", "/verysilent":
			silent = true
		case "--repair", "/repair":
			repairOnly = true
		case "--remove-data", "--purge":
			keepData = false
		case "--keep-data":
			keepData = true
		}
	}

	if repairOnly {
		runRepair(root, silent)
		return
	}

	if !silent {
		ans := winui.MessageBox(
			title,
			fmt.Sprintf(
				"Manage Darius DB (Lite Version %s)\n\nInstall folder:\n%s\n\n"+
					"• Click YES to UNINSTALL Darius DB\n"+
					"• Click NO to REPAIR Darius DB (fix errors, configs, stale locks & shortcuts while keeping all your databases)\n"+
					"• Click CANCEL to exit",
				appVersion, root,
			),
			winui.MBYesNoCancel|winui.MBIconQuestion,
		)
		if ans == winui.IDCANCEL {
			return
		}
		if ans == winui.IDNO {
			runRepair(root, false)
			return
		}

		dataChoice := winui.MessageBox(
			title,
			"Do you want to KEEP your database data and config files (data/, etc/, settings.json)?\n\n• Click YES to keep your databases safe (recommended).\n• Click NO to permanently delete all data and settings.\n• Click CANCEL to abort uninstall.",
			winui.MBYesNoCancel|winui.MBIconQuestion,
		)
		if dataChoice == winui.IDCANCEL {
			return
		}
		keepData = (dataChoice == winui.IDYES)
	}

	// 1. Terminate any processes launched from this install folder (except ourselves)
	winui.KillProcessesInDir(root, uint32(os.Getpid()))
	time.Sleep(500 * time.Millisecond)

	// 2. Remove Windows Startup entry and Apps & Features registry key
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue("DariusDB")
		k.Close()
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstallKey)

	// 3. Remove Desktop & Start Menu shortcuts
	removeShortcuts()

	// 4. Remove program files & directories
	programItems := []string{
		"DariusDB.exe",
		"DariusDB-Launcher.exe",
		"darius.exe",
		"LICENSE",
		"README.md",
		"GUIDE.md",
		"catalog.json",
		"bin",
		"tmp",
		"logs",
	}
	if !keepData {
		programItems = append(programItems, "data", "etc", "settings.json")
	}
	for _, name := range programItems {
		_ = os.RemoveAll(filepath.Join(root, name))
	}

	if !silent {
		msg := "Darius DB has been successfully uninstalled."
		if keepData {
			msg += fmt.Sprintf("\n\nYour database files were kept safely in:\n%s", filepath.Join(root, "data"))
		} else {
			msg += "\n\nAll Darius DB files and data have been removed."
		}
		winui.MessageBox(title, msg, winui.MBOK|winui.MBIconInfo)
	}

	// 5. Schedule self-delete of DariusDB-Uninstall.exe (and empty root folder if purged)
	scheduleSelfDelete(selfExe, root, !keepData)
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

func scheduleSelfDelete(exePath, root string, removeRoot bool) {
	if exePath == "" {
		return
	}
	cmdStr := fmt.Sprintf(`ping -n 3 127.0.0.1 >nul & del /f /q "%s"`, exePath)
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

func runRepair(root string, silent bool) {
	for _, sub := range []string{"www", "bin", "data", "etc", "logs", "tmp"} {
		_ = os.MkdirAll(filepath.Join(root, sub), 0o755)
	}
	cliExe := filepath.Join(root, "darius.exe")
	summary := "Repaired portable directories (www/, bin/, data/, etc/, logs/, tmp/)."
	if st, err := os.Stat(cliExe); err == nil && !st.IsDir() {
		cmd := exec.Command(cliExe, "repair", "all")
		cmd.Dir = root
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if out, err := cmd.CombinedOutput(); err == nil && len(out) > 0 {
			summary = strings.TrimSpace(string(out))
		}
	}
	if !silent {
		winui.MessageBox(
			"Repair Darius DB (Lite Version "+appVersion+")",
			"Darius DB Repair Completed!\n\n"+summary,
			winui.MBOK|winui.MBIconInfo,
		)
	}
}
