//go:build windows

package tray

import (
	"testing"
	"unsafe"
)

func TestWin64StructSizes(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("amd64/arm64 size checks only")
	}
	if got := unsafe.Sizeof(wndClassExW{}); got != 80 {
		t.Fatalf("wndClassExW size = %d, want 80", got)
	}
	if got := unsafe.Sizeof(msg{}); got != 48 {
		t.Fatalf("msg size = %d, want 48", got)
	}
	if got := unsafe.Sizeof(notifyIconDataW{}); got != 976 {
		t.Fatalf("notifyIconDataW size = %d, want 976", got)
	}
}

func TestTrayStartAndClose(t *testing.T) {
	tr, err := Start(Config{
		Tag:     "test-unit",
		Tooltip: "Darius DB Test",
		IconPNG: DefaultIconPNG,
	})
	if err != nil {
		t.Fatalf("tray.Start failed: %v", err)
	}
	if tr.hicon == 0 {
		t.Error("expected HICON to be created from embedded PNG")
	}
	tr.SetTooltip("Updated tooltip")
	tr.Close()
}
