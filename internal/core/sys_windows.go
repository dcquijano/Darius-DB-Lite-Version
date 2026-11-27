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

// killProcessTree terminates pid and all of its child/descendant processes
// (critical for PostgreSQL postmaster + workers, Apache workers, etc.).
func killProcessTree(pid int) {
	if pid <= 0 || pid == os.Getpid() {
		return
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err == nil {
		defer windows.CloseHandle(snap)
		children := map[uint32][]uint32{}
		var pe windows.ProcessEntry32
		pe.Size = uint32(unsafe.Sizeof(pe))
		if windows.Process32First(snap, &pe) == nil {
			for {
				if pe.ProcessID != 0 {
					children[pe.ParentProcessID] = append(children[pe.ParentProcessID], pe.ProcessID)
				}
				if windows.Process32Next(snap, &pe) != nil {
					break
				}
			}
		}
		var order []uint32
		seen := map[uint32]bool{uint32(pid): true}
		queue := []uint32{uint32(pid)}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, ch := range children[cur] {
				if !seen[ch] {
					seen[ch] = true
					order = append(order, ch)
					queue = append(queue, ch)
				}
			}
		}
		for i := len(order) - 1; i >= 0; i-- {
			_ = killProcess(int(order[i]))
		}
	}
	_ = killProcess(pid)
}

// pidsInDir returns PIDs of running processes (excluding self) whose executable resides under dir.
func pidsInDir(dir string) []int {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	prefix := strings.ToLower(filepath.Clean(dir)) + string(filepath.Separator)
	selfPid := uint32(os.Getpid())
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil
	}
	var out []int
	for {
		pid := pe.ProcessID
		if pid != 0 && pid != selfPid {
			p := strings.ToLower(filepath.Clean(processPath(int(pid))))
			if p != "" && strings.HasPrefix(p, prefix) {
				out = append(out, int(pid))
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return out
}

// hasProcessInDir reports whether any process whose executable resides under dir is alive.
func hasProcessInDir(dir string) bool {
	return len(pidsInDir(dir)) > 0
}

// killProcessesInDir terminates any running process whose executable lives inside dir.
func killProcessesInDir(dir string) {
	for _, pid := range pidsInDir(dir) {
		_ = killProcess(pid)
	}
}

// clearReadOnlyRecursive clears read-only attributes on all files/folders under dir so os.RemoveAll succeeds.
func clearReadOnlyRecursive(dir string) {
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil {
			_ = os.Chmod(path, 0o777)
		}
		return nil
	})
}

func portInUse(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 45*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// ---------------------------------------------------------------- single instance

var (
	user32Sys               = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW         = user32Sys.NewProc("FindWindowW")
	procPostMessageW        = user32Sys.NewProc("PostMessageW")
	procSendMessageW        = user32Sys.NewProc("SendMessageW")
	procRegisterWindowMsgW  = user32Sys.NewProc("RegisterWindowMessageW")
	procCreateIconFromResEx = user32Sys.NewProc("CreateIconFromResourceEx")
	procSetClassLongPtrW    = user32Sys.NewProc("SetClassLongPtrW")
	procShowWindow          = user32Sys.NewProc("ShowWindow")
	procSetForegroundWindow = user32Sys.NewProc("SetForegroundWindow")
	procBringWindowToTop    = user32Sys.NewProc("BringWindowToTop")
	procIsIconic            = user32Sys.NewProc("IsIconic")
)

// ForceShowWindow locates the top-level window matching windowTitle and brings it
// visibly to the foreground (restoring from hidden/minimized state if needed).
func ForceShowWindow(windowTitle string) {
	if windowTitle == "" {
		return
	}
	titlePtr, err := windows.UTF16PtrFromString(windowTitle)
	if err != nil {
		return
	}
	const (
		swShow    = 5
		swRestore = 9
	)
	for i := 0; i < 60; i++ {
		hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
		if hwnd != 0 {
			if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
				procShowWindow.Call(hwnd, swRestore)
			} else {
				procShowWindow.Call(hwnd, swShow)
			}
			procBringWindowToTop.Call(hwnd)
			procSetForegroundWindow.Call(hwnd)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ApplyWindowIconFromPNG sets both the window instance icon (WM_SETICON) and
// window class icon (GCL_HICON / GCL_HICONSM) for the top-level window matching
// windowTitle so the titlebar, taskbar, and Alt+Tab switcher always display the
// Darius DB logo.
func ApplyWindowIconFromPNG(windowTitle string, pngBytes []byte) {
	if len(pngBytes) == 0 || windowTitle == "" {
		return
	}
	hIconBig, _, _ := procCreateIconFromResEx.Call(
		uintptr(unsafe.Pointer(&pngBytes[0])),
		uintptr(len(pngBytes)),
		1,
		0x00030000,
		48, 48,
		0,
	)
	hIconSmall, _, _ := procCreateIconFromResEx.Call(
		uintptr(unsafe.Pointer(&pngBytes[0])),
		uintptr(len(pngBytes)),
		1,
		0x00030000,
		24, 24,
		0,
	)
	if hIconBig == 0 && hIconSmall == 0 {
		return
	}
	titlePtr, err := windows.UTF16PtrFromString(windowTitle)
	if err != nil {
		return
	}
	const (
		wmSetIcon  = 0x0080
		iconSmall  = 0
		iconBig    = 1
		gclHIcon   = ^uintptr(14 - 1) // -14
		gclHIconSm = ^uintptr(34 - 1) // -34
	)
	for i := 0; i < 40; i++ {
		hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
		if hwnd != 0 {
			if hIconBig != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hIconBig)
				procSetClassLongPtrW.Call(hwnd, gclHIcon, hIconBig)
			}
			if hIconSmall != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hIconSmall)
				procSetClassLongPtrW.Call(hwnd, gclHIconSm, hIconSmall)
			}
			time.Sleep(250 * time.Millisecond)
			if hIconBig != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hIconBig)
			}
			if hIconSmall != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hIconSmall)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func InstanceTag(root string) string {
	h := sha1.Sum([]byte(strings.ToLower(filepath.Clean(root))))
	return hex.EncodeToString(h[:8])
}

// AcquireSingleInstance acquires a per-portable-root mutex.
// If another instance is already running for this root, it signals that
// instance's tray window to show itself and returns (0, false).
func AcquireSingleInstance(root string) (windows.Handle, bool) {
	return AcquireSingleInstanceWithWait(root, 0)
}

// AcquireSingleInstanceWithWait acquires the single-instance mutex, optionally
// waiting up to waitTimeout for a previous instance to finish exiting (used after update-reboots).
func AcquireSingleInstanceWithWait(root string, waitTimeout time.Duration) (windows.Handle, bool) {
	tag := InstanceTag(root)
	name, _ := windows.UTF16PtrFromString("Local\\DariusDB-" + tag)
	deadline := time.Now().Add(waitTimeout)
	for {
		h, err := windows.CreateMutex(nil, false, name)
		if err == windows.ERROR_ALREADY_EXISTS {
			if h != 0 {
				windows.CloseHandle(h)
			}
			if waitTimeout > 0 && time.Now().Before(deadline) {
				time.Sleep(150 * time.Millisecond)
				continue
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
