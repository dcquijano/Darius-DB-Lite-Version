//go:build windows

package core

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// processAlive reports whether pid is running.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// processPath returns the full exe path of pid.
func processPath(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// processBelongsTo reports whether pid is alive and its exe lives under root.
func processBelongsTo(pid int, root string) bool {
	if !processAlive(pid) {
		return false
	}
	path := strings.ToLower(filepath.Clean(processPath(pid)))
	return path != "" && strings.HasPrefix(path, strings.ToLower(filepath.Clean(root))+string(filepath.Separator))
}

func killProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func portInUse(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// ---------------------------------------------------------------- single instance

var (
	user32Sys              = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW        = user32Sys.NewProc("FindWindowW")
	procPostMessageW       = user32Sys.NewProc("PostMessageW")
	procRegisterWindowMsgW = user32Sys.NewProc("RegisterWindowMessageW")
)

func InstanceTag(root string) string {
	h := sha1.Sum([]byte(strings.ToLower(filepath.Clean(root))))
	return hex.EncodeToString(h[:8])
}

// AcquireSingleInstance acquires a per-portable-root mutex.
// If another instance is already running for this root, it signals that
// instance's tray window to show itself and returns (0, false).
func AcquireSingleInstance(root string) (windows.Handle, bool) {
	tag := InstanceTag(root)
	name, _ := windows.UTF16PtrFromString("Local\\DariusDB-" + tag)
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			windows.CloseHandle(h)
		}
		cls, _ := windows.UTF16PtrFromString("DariusDBTray-" + tag)
		hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(cls)), 0)
		if hwnd != 0 {
			msgName, _ := windows.UTF16PtrFromString("DariusDBShow-" + tag)
			msg, _, _ := procRegisterWindowMsgW.Call(uintptr(unsafe.Pointer(msgName)))
			if msg != 0 {
				procPostMessageW.Call(hwnd, msg, 0, 0)
			}
		}
		return 0, false
	}
	if err != nil {
		return 0, true // don't block startup if mutex unexpectedly fails
	}
	return h, true
}

func ReleaseSingleInstance(h windows.Handle) {
	if h != 0 {
		windows.CloseHandle(h)
	}
}

// ---------------------------------------------------------------- autostart

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "DariusDB"

func SetRunOnBoot(enable bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enable {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, fmt.Sprintf(`"%s" --minimized`, exe))
}

// ---------------------------------------------------------------- shell helpers

func OpenFolder(path string) error {
	_ = os.MkdirAll(path, 0o755)
	return exec.Command("explorer.exe", path).Start()
}

func OpenInNotepad(path string) error {
	return exec.Command("notepad.exe", path).Start()
}

func escapeCmdEcho(s string) string {
	r := strings.NewReplacer(
		"^", "^^",
		"&", "^&",
		"|", "^|",
		"<", "^<",
		">", "^>",
		"(", "^(",
		")", "^)",
		`"`, `'`,
	)
	return r.Replace(s)
}

// OpenTerminal opens a new external console window with the given PATH prefix and env.
func OpenTerminal(dir string, pathPrefix []string, env map[string]string, banner []string) error {
	initDir := filepath.Join(dir, "tmp")
	_ = os.MkdirAll(initDir, 0o755)
	initCmd := filepath.Join(initDir, "terminal-init.cmd")

	var script strings.Builder
	script.WriteString("@echo off\r\ntitle Darius DB Terminal\r\n")
	for _, line := range banner {
		script.WriteString("echo " + escapeCmdEcho(line) + "\r\n")
	}
	script.WriteString("echo.\r\n")
	_ = os.WriteFile(initCmd, []byte(script.String()), 0o644)

	envList := os.Environ()
	newPath := strings.Join(append(pathPrefix, os.Getenv("PATH")), ";")
	out := make([]string, 0, len(envList)+len(env)+1)
	for _, kv := range envList {
		k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if k == "PATH" {
			continue
		}
		if _, override := env[k]; override {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PATH="+newPath)
	for k, v := range env {
		out = append(out, k+"="+v)
	}

	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	cmd := exec.Command(cmdExe, "/C", "start", "Darius DB Terminal", cmdExe, "/K", initCmd)
	cmd.Dir = dir
	cmd.Env = out
	hideWindow(cmd)
	return cmd.Start()
}
