//go:build windows

package winui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	MBOK          = 0x00000000
	MBYesNo       = 0x00000004
	MBYesNoCancel = 0x00000003

	MBIconError    = 0x00000010
	MBIconQuestion = 0x00000020
	MBIconWarning  = 0x00000030
	MBIconInfo     = 0x00000040

	IDOK     = 1
	IDCANCEL = 2
	IDYES    = 6
	IDNO     = 7
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	ole32                    = windows.NewLazySystemDLL("ole32.dll")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procOleInitialize        = ole32.NewProc("OleInitialize")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procCoUninitialize       = ole32.NewProc("CoUninitialize")
	procCoCreateInstance     = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

type browseInfoW struct {
	hwndOwner      syscall.Handle
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// BrowseForFolder opens the native Windows folder picker dialog.
// Returns "" if the user cancelled.
func BrowseForFolder(title string) string {
	procOleInitialize.Call(0)
	displayBuf := make([]uint16, windows.MAX_PATH)
	titlePtr, _ := windows.UTF16PtrFromString(title)
	bi := browseInfoW{
		pszDisplayName: &displayBuf[0],
		lpszTitle:      titlePtr,
		ulFlags:        0x00000001 | 0x00000040, // BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE
	}
	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer procCoTaskMemFree.Call(pidl)
	pathBuf := make([]uint16, windows.MAX_PATH)
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return ""
	}
	return windows.UTF16ToString(pathBuf)
}

func MessageBox(title, text string, flags uint32) int {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(flags))
	return int(ret)
}

func OpenURL(url string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

// WebView2Version checks whether Microsoft Edge WebView2 Runtime is available
// and returns (installed, version).
func WebView2Version() (bool, string) {
	const guid = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	checks := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + guid},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + guid},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\` + guid},
	}
	for _, c := range checks {
		k, err := registry.OpenKey(c.root, c.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		pv, _, err := k.GetStringValue("pv")
		k.Close()
		pv = strings.TrimSpace(pv)
		if err == nil && pv != "" && pv != "0.0.0.0" {
			return true, pv
		}
	}
	return false, ""
}

// WebView2Installed checks whether Microsoft Edge WebView2 Runtime is available.
func WebView2Installed() bool {
	ok, _ := WebView2Version()
	return ok
}

// KillProcessesInDir terminates processes whose image path is inside root,
// skipping selfPID.
func KillProcessesInDir(root string, selfPID uint32) {
	prefix := strings.ToLower(filepath.Clean(root)) + string(filepath.Separator)
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return
	}
	for {
		pid := pe.ProcessID
		if pid != 0 && pid != selfPID {
			if p := imagePath(pid); p != "" {
				if strings.HasPrefix(strings.ToLower(filepath.Clean(p)), prefix) {
					if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
						_ = windows.TerminateProcess(h, 0)
						windows.CloseHandle(h)
					}
				}
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
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

type comVtbl struct {
	vtbl *[32]uintptr
}

func comCall(obj uintptr, slot int, args ...uintptr) uintptr {
	tbl := (*comVtbl)(unsafe.Pointer(obj)).vtbl
	callArgs := make([]uintptr, 1+len(args))
	callArgs[0] = obj
	copy(callArgs[1:], args)
	r1, _, _ := syscall.SyscallN(tbl[slot], callArgs...)
	return r1
}

// CreateShortcut creates a Windows .lnk shortcut using native COM (IShellLinkW + IPersistFile),
// avoiding spawning hidden powershell.exe/wscript child processes during installation.
func CreateShortcut(lnkPath, targetPath, args, workDir, iconPath, desc string) error {
	hr, _, _ := procCoInitializeEx.Call(0, 0x2) // COINIT_APARTMENTTHREADED
	if hr == 0 || hr == 1 {                     // S_OK or S_FALSE
		defer procCoUninitialize.Call()
	}

	clsidShellLink := windows.GUID{Data1: 0x00021401, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIShellLinkW := windows.GUID{Data1: 0x000214F9, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIPersistFile := windows.GUID{Data1: 0x0000010B, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}

	var sl uintptr
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidIShellLinkW)),
		uintptr(unsafe.Pointer(&sl)),
	)
	if hr != 0 || sl == 0 {
		return syscall.Errno(hr)
	}
	defer comCall(sl, 2) // Release

	setStr := func(slot int, val string) {
		if val == "" {
			return
		}
		p, _ := windows.UTF16PtrFromString(val)
		comCall(sl, slot, uintptr(unsafe.Pointer(p)))
	}
	setStr(20, targetPath) // SetPath
	setStr(11, args)       // SetArguments
	setStr(9, workDir)     // SetWorkingDirectory
	setStr(7, desc)        // SetDescription
	if iconPath != "" {
		ip, _ := windows.UTF16PtrFromString(iconPath)
		comCall(sl, 17, uintptr(unsafe.Pointer(ip)), 0) // SetIconLocation
	}

	var pf uintptr
	hr = comCall(sl, 0, uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&pf))) // QueryInterface
	if hr != 0 || pf == 0 {
		return syscall.Errno(hr)
	}
	defer comCall(pf, 2) // Release

	lp, err := windows.UTF16PtrFromString(lnkPath)
	if err != nil {
		return err
	}
	hr = comCall(pf, 6, uintptr(unsafe.Pointer(lp)), 1) // Save(lnkPath, TRUE)
	if hr != 0 {
		return syscall.Errno(hr)
	}
	return nil
}
