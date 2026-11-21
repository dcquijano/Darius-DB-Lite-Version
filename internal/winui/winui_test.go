//go:build windows

package winui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWebView2Installed(t *testing.T) {
	// Just ensures registry lookup works cleanly without panicking.
	_ = WebView2Installed()
}

func TestCreateShortcut(t *testing.T) {
	dir := t.TempDir()
	lnk := filepath.Join(dir, "TestApp.lnk")
	target := filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe")
	if err := CreateShortcut(lnk, target, "", dir, target, "Test shortcut"); err != nil {
		t.Fatalf("CreateShortcut failed: %v", err)
	}
	st, err := os.Stat(lnk)
	if err != nil || st.Size() == 0 {
		t.Fatalf("expected non-empty shortcut file at %s, err=%v", lnk, err)
	}
}
