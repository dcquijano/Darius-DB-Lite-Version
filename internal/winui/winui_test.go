//go:build windows

package winui

import "testing"

func TestWebView2Installed(t *testing.T) {
	// Just ensures registry lookup works cleanly without panicking.
	_ = WebView2Installed()
}
