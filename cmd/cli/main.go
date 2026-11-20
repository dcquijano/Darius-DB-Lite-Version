//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func main() {
	root := resolveRoot()
	exe := findAppExe(root)
	if exe == "" {
		fmt.Fprintf(os.Stderr, "Error: DariusDB.exe not found in %s\n", root)
		os.Exit(1)
	}

	args := os.Args[1:]
	if len(args) == 1 {
		sub := strings.ToLower(strings.TrimLeft(args[0], "-"))
		if sub == "gui" || sub == "open" || sub == "ui" {
			cmd := exec.Command(exe)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "DARIUSDB_HOME="+root)
			_ = cmd.Start()
			fmt.Println("Opened Darius DB (Lite Version 0.1.4) desktop window.")
			return
		}
	}

	cliArgs := append([]string{"--cli"}, args...)
	cmd := exec.Command(exe, cliArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DARIUSDB_HOME="+root)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
