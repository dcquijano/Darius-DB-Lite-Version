//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"dariusdb/internal/core"
	"dariusdb/internal/winui"
)

const (
	appTitle    = "Darius DB (Lite Version 0.1.0) Launcher"
	appVersion  = "0.1.0"
	appAuthor   = "Darius Cumpio"
	appCreated  = "November 19, 2026"
	appRepo     = "https://github.com/dcquijano/Darius-DB-Lite-Version"
	webview2URL = "https://developer.microsoft.com/en-us/microsoft-edge/webview2/"
)

func main() {
	root := core.ResolveRoot()
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
		case "--start-all":
			runHeadless(root, true)
			return
		case "--stop-all":
			runHeadless(root, false)
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
	candidates := []string{
		filepath.Join(root, "DariusDB.exe"),
		filepath.Join(root, "app", "DariusDB.exe"),
		filepath.Join(root, "build", "bin", "DariusDB.exe"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func openTerminal(root string) {
	paths := core.NewPaths(root)
	_ = paths.Ensure()
	st := core.LoadSettings(root)
	mgr := core.NewManager(paths, st, nil)
	s := st.Get()

	env := map[string]string{"DARIUSDB_HOME": root}
	if pg := s.Engines["postgresql"]; pg != nil {
		env["PGHOST"] = "127.0.0.1"
		env["PGPORT"] = strconv.Itoa(pg.Port)
		if pg.User != "" {
			env["PGUSER"] = pg.User
		}
	}
	if m := s.Engines["mariadb"]; m != nil && m.Version != "" {
		env["MYSQL_HOST"] = "127.0.0.1"
		env["MYSQL_TCP_PORT"] = strconv.Itoa(m.Port)
	} else if m := s.Engines["mysql"]; m != nil && m.Version != "" {
		env["MYSQL_HOST"] = "127.0.0.1"
		env["MYSQL_TCP_PORT"] = strconv.Itoa(m.Port)
	}

	banner := []string{
		fmt.Sprintf("Darius DB (Lite Version %s) Terminal - by %s", appVersion, appAuthor),
	}
	for _, info := range mgr.Status() {
		if info.Active != "" {
			banner = append(banner, fmt.Sprintf("  %-11s %-18s port %-5d", info.Name, info.Active, info.Port))
		}
	}
	if err := core.OpenTerminal(root, mgr.ClientPaths(), env, banner); err != nil {
		winui.MessageBox(appTitle, fmt.Sprintf("Failed to open terminal:\n%v", err), winui.MBOK|winui.MBIconError)
	}
}

func runHeadless(root string, start bool) {
	paths := core.NewPaths(root)
	_ = paths.Ensure()
	st := core.LoadSettings(root)
	mgr := core.NewManager(paths, st, nil)
	var err error
	if start {
		err = mgr.StartAll()
	} else {
		err = mgr.StopAll()
	}
	if err != nil {
		winui.MessageBox(appTitle, err.Error(), winui.MBOK|winui.MBIconWarning)
	}
}
