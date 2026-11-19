//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"dariusdb/internal/winui"
)

const (
	appTitle    = "Darius DB (Lite Version 0.1.1) Launcher"
	appVersion  = "0.1.1"
	appAuthor   = "Darius Cumpio"
	appCreated  = "November 19, 2026"
	appRepo     = "https://github.com/dcquijano/Darius-DB-Lite-Version"
	webview2URL = "https://developer.microsoft.com/en-us/microsoft-edge/webview2/"
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
	args := os.Args[1:]

	for _, a := range args {
		switch a {
		case "--version", "-v":
			winui.MessageBox(
				appTitle,
				fmt.Sprintf("Darius DB (Lite Version %s)\n\nCreated by: %s\nCreated: %s\nLicense: MIT (Open Source)\nRepository: %s", appVersion, appAuthor, appCreated, appRepo),
				winui.MBOK|winui.MBIconInfo,
			)
			return
		case "--terminal", "-t":
			openTerminal(root)
			return
		case "--stop-all":
			winui.KillProcessesInDir(filepath.Join(root, "bin"), uint32(os.Getpid()))
			return
		}
	}

	if !winui.WebView2Installed() {
		choice := winui.MessageBox(
			appTitle,
			"Microsoft Edge WebView2 Runtime is required to run Darius DB.\n\nWould you like to open the official Microsoft download page?",
			winui.MBYesNo|winui.MBIconWarning,
		)
		if choice == winui.IDYES {
			_ = winui.OpenURL(webview2URL)
		}
		return
	}

	exePath := findAppExe(root)
	if exePath == "" {
		winui.MessageBox(
			appTitle,
			fmt.Sprintf("Could not find DariusDB.exe next to the launcher.\n\nExpected in:\n%s", root),
			winui.MBOK|winui.MBIconError,
		)
		return
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DARIUSDB_HOME="+root)
	if err := cmd.Start(); err != nil {
		winui.MessageBox(appTitle, fmt.Sprintf("Failed to launch Darius DB:\n%v", err), winui.MBOK|winui.MBIconError)
	}
}

func findAppExe(root string) string {
	for _, p := range []string{
		filepath.Join(root, "DariusDB.exe"),
		filepath.Join(root, "app", "DariusDB.exe"),
		filepath.Join(root, "build", "bin", "DariusDB.exe"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func openTerminal(root string) {
	var binDirs []string
	for _, eng := range []string{"mariadb", "mysql", "postgresql", "mssql", "redis"} {
		base := filepath.Join(root, "bin", eng)
		entries, _ := os.ReadDir(base)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(base, e.Name(), "bin")
			if st, err := os.Stat(sub); err == nil && st.IsDir() {
				binDirs = append(binDirs, sub)
			} else {
				binDirs = append(binDirs, filepath.Join(base, e.Name()))
			}
		}
	}

	initDir := filepath.Join(root, "tmp")
	_ = os.MkdirAll(initDir, 0o755)
	initCmd := filepath.Join(initDir, "terminal-init.cmd")
	script := fmt.Sprintf("@echo off\r\ntitle Darius DB (Lite Version %s) Terminal\r\necho Darius DB (Lite Version %s) Terminal - by %s\r\necho.\r\n", appVersion, appVersion, appAuthor)
	_ = os.WriteFile(initCmd, []byte(script), 0o644)

	newPath := strings.Join(append(binDirs, os.Getenv("PATH")), ";")
	envList := os.Environ()
	out := make([]string, 0, len(envList)+2)
	for _, kv := range envList {
		if !strings.EqualFold(strings.SplitN(kv, "=", 2)[0], "PATH") {
			out = append(out, kv)
		}
	}
	out = append(out, "PATH="+newPath, "DARIUSDB_HOME="+root)

	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd := exec.Command(cmdExe, "/C", "start", "Darius DB Terminal", cmdExe, "/K", initCmd)
	cmd.Dir = root
	cmd.Env = out
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		winui.MessageBox(appTitle, fmt.Sprintf("Failed to open terminal:\n%v", err), winui.MBOK|winui.MBIconError)
	}
}
