//go:build windows

package main

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
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
	appShortName = "Darius DB"
	appFullTitle = "Darius DB (Lite Version 0.1.1)"
	appVersion   = "0.1.1"
	appAuthor    = "Darius Cumpio"
	defaultDir   = `C:\DariusDB`
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\DariusDB`
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	trailerMagic = "DARIUS01"
)

func main() {
	installDir := defaultDir
	silent := false
	autoStart := false
	noDesktop := false
	noLaunch := false

	for _, a := range os.Args[1:] {
		low := strings.ToLower(a)
		switch {
		case low == "--silent" || low == "/s" || low == "/verysilent":
			silent = true
		case low == "--autostart":
			autoStart = true
		case low == "--no-desktop":
			noDesktop = true
		case low == "--no-launch":
			noLaunch = true
		case strings.HasPrefix(low, "--dir="):
			installDir = strings.Trim(a[6:], `"`)
		}
	}

	if !silent {
		ans := winui.MessageBox(
			appFullTitle+" Setup",
			fmt.Sprintf(
				"Welcome to %s Setup!\n\nCreated by: %s (MIT Open Source)\n\nDefault install folder:\n%s\n\n• Click YES to install to %s (recommended)\n• Click NO to choose a different folder\n• Click CANCEL to quit setup",
				appFullTitle, appAuthor, installDir, installDir,
			),
			winui.MBYesNoCancel|winui.MBIconQuestion,
		)
		if ans == winui.IDCANCEL {
			return
		}
		if ans == winui.IDNO {
			picked := winui.BrowseForFolder("Select destination folder for " + appFullTitle)
			if picked == "" {
				return
			}
			if !strings.EqualFold(filepath.Base(picked), "DariusDB") {
				picked = filepath.Join(picked, "DariusDB")
			}
			installDir = picked
		}

		bootAns := winui.MessageBox(
			appFullTitle+" Setup",
			"Start Darius DB automatically when Windows signs in (minimized to system tray)?",
			winui.MBYesNo|winui.MBIconQuestion,
		)
		autoStart = (bootAns == winui.IDYES)
	}

	if err := performInstall(installDir, autoStart, !noDesktop); err != nil {
		if !silent {
			winui.MessageBox(appFullTitle+" Setup", "Installation failed:\n\n"+err.Error(), winui.MBOK|winui.MBIconError)
		}
		os.Exit(1)
	}

	if !silent && !noLaunch {
		launchAns := winui.MessageBox(
			appFullTitle+" Setup",
			fmt.Sprintf(
				"%s has been installed successfully!\n\nFolder: %s\nShortcuts: Desktop & Start Menu\nUninstall: Windows Settings > Apps > Installed Apps\n\nLaunch %s now?",
				appFullTitle, installDir, appFullTitle,
			),
			winui.MBYesNo|winui.MBIconInfo,
		)
		if launchAns == winui.IDYES {
			launcher := filepath.Join(installDir, "DariusDB-Launcher.exe")
			if !fileExists(launcher) {
				launcher = filepath.Join(installDir, "DariusDB.exe")
			}
			cmd := exec.Command(launcher)
			cmd.Dir = installDir
			_ = cmd.Start()
		}
	}
}

func performInstall(installDir string, autoStart, createDesktop bool) error {
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("cannot create folder %s: %w", installDir, err)
	}

	// Gracefully stop any running processes in installDir before overwriting
	existingLauncher := filepath.Join(installDir, "DariusDB-Launcher.exe")
	if fileExists(existingLauncher) {
		stopCmd := exec.Command(existingLauncher, "--stop-all")
		stopCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = stopCmd.Run()
	}
	winui.KillProcessesInDir(installDir, uint32(os.Getpid()))
	time.Sleep(500 * time.Millisecond)

	// Extract from appended ZIP payload if present; otherwise copy from disk
	extracted, err := extractEmbeddedZip(installDir)
	if err != nil {
		return err
	}
	if !extracted {
		if err := copyFromLocalBuild(installDir); err != nil {
			return err
		}
	}

	for _, sub := range []string{"bin", "data", "etc", "logs", "tmp"} {
		if err := os.MkdirAll(filepath.Join(installDir, sub), 0o755); err != nil {
			return err
		}
	}

	createShortcuts(installDir, createDesktop)
	registerUninstall(installDir)
	configureAutoStart(installDir, autoStart)
	addInstallDirToUserPath(installDir)
	return nil
}

// extractEmbeddedZip reads a ZIP payload appended to the end of this .exe.
func extractEmbeddedZip(installDir string) (bool, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return false, nil
	}
	f, err := os.Open(selfExe)
	if err != nil {
		return false, nil
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.Size() < 32 {
		return false, nil
	}
	var trailer [16]byte
	if _, err := f.ReadAt(trailer[:], st.Size()-16); err != nil {
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

	zr, err := zip.NewReader(io.NewSectionReader(f, zipOffset, zipSize), zipSize)
	if err != nil {
		return false, fmt.Errorf("cannot read installer payload: %w", err)
	}

	allowed := map[string]bool{
		"DariusDB.exe":           true,
		"DariusDB-Launcher.exe":  true,
		"DariusDB-Uninstall.exe": true,
		"darius.exe":             true,
		"LICENSE":                true,
		"README.md":              true,
		"GUIDE.md":               true,
	}
	for _, zf := range zr.File {
		base := filepath.Base(zf.Name)
		if !allowed[base] {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return false, err
		}
		dest := filepath.Join(installDir, base)
		out, err := os.Create(dest)
		if err != nil {
			rc.Close()
			return false, err
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

func copyFromLocalBuild(installDir string) error {
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

	mainSrc := findFile("DariusDB.exe")
	if mainSrc == "" {
		return fmt.Errorf("DariusDB.exe was not found next to the installer or in build\\bin\\")
	}
	for _, name := range []string{"DariusDB.exe", "DariusDB-Launcher.exe", "DariusDB-Uninstall.exe", "darius.exe", "LICENSE", "README.md", "GUIDE.md"} {
		src := findFile(name)
		if src == "" {
			continue
		}
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

func createShortcuts(installDir string, createDesktop bool) {
	mainExe := filepath.Join(installDir, "DariusDB.exe")
	launcherExe := filepath.Join(installDir, "DariusDB-Launcher.exe")
	if !fileExists(launcherExe) {
		launcherExe = mainExe
	}
	uninstExe := filepath.Join(installDir, "DariusDB-Uninstall.exe")
	startMenu := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", appShortName)
	_ = os.MkdirAll(startMenu, 0o755)

	ps := fmt.Sprintf(`$w = New-Object -ComObject WScript.Shell; `+
		`function mk($l,$t,$a,$d){ $s=$w.CreateShortcut($l); $s.TargetPath=$t; $s.Arguments=$a; $s.WorkingDirectory='%s'; $s.IconLocation='%s,0'; $s.Description=$d; $s.Save() }; `+
		`mk '%s' '%s' '' 'Launch %s'; `+
		`mk '%s' '%s' '--terminal' 'Open %s Terminal'; `+
		`mk '%s' '%s' '' 'Uninstall %s';`,
		escPS(installDir), escPS(mainExe),
		escPS(filepath.Join(startMenu, appShortName+".lnk")), escPS(launcherExe), escPS(appFullTitle),
		escPS(filepath.Join(startMenu, appShortName+" Terminal.lnk")), escPS(launcherExe), escPS(appFullTitle),
		escPS(filepath.Join(startMenu, "Uninstall "+appShortName+".lnk")), escPS(uninstExe), escPS(appFullTitle),
	)
	if createDesktop {
		desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
		if st, err := os.Stat(desktop); err == nil && st.IsDir() {
			ps += fmt.Sprintf(` mk '%s' '%s' '' 'Launch %s';`,
				escPS(filepath.Join(desktop, appShortName+".lnk")), escPS(launcherExe), escPS(appFullTitle))
		}
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	_ = cmd.Run()
}

func escPS(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func registerUninstall(installDir string) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()

	mainExe := filepath.Join(installDir, "DariusDB.exe")
	uninstExe := filepath.Join(installDir, "DariusDB-Uninstall.exe")
	_ = k.SetStringValue("DisplayName", appFullTitle)
	_ = k.SetStringValue("DisplayVersion", appVersion)
	_ = k.SetStringValue("Publisher", appAuthor)
	_ = k.SetStringValue("InstallLocation", installDir)
	_ = k.SetStringValue("DisplayIcon", mainExe+",0")
	_ = k.SetStringValue("InstallDate", time.Now().Format("20060102"))
	_ = k.SetDWordValue("EstimatedSize", 28000)
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	_ = k.SetStringValue("UninstallString", fmt.Sprintf(`"%s"`, uninstExe))
	_ = k.SetStringValue("QuietUninstallString", fmt.Sprintf(`"%s" --silent --keep-data`, uninstExe))
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

func addInstallDirToUserPath(installDir string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	cur, _, _ := k.GetStringValue("Path")
	cleanTarget := strings.ToLower(strings.TrimRight(filepath.Clean(installDir), `\/`))
	var parts []string
	for _, p := range strings.Split(cur, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.ToLower(strings.TrimRight(filepath.Clean(p), `\/`)) == cleanTarget {
			return // already in PATH
		}
		parts = append(parts, p)
	}
	parts = append([]string{filepath.Clean(installDir)}, parts...)
	_ = k.SetExpandStringValue("Path", strings.Join(parts, ";"))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
