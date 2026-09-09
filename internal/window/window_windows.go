//go:build windows

package window

import (
	"log"
	"os/exec"
	"syscall"

	"github.com/jchv/go-webview2"
)

// Open shows the UI in a native window backed by the Microsoft Edge WebView2
// runtime (pre-installed on Windows 10 21H2+ and Windows 11). It blocks until
// that window is closed. If WebView2 is unavailable it hands the interface to
// the default browser instead, whose lifetime is not ours to observe.
func Open(url, title, dataPath string, width, height int) Mode {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			Center: true,
		},
	})
	if w == nil {
		log.Printf("WebView2 runtime not available; falling back to the default browser")
		OpenBrowser(url)
		return Handed
	}
	defer w.Destroy()
	w.SetSize(width, height, webview2.HintNone)
	w.Navigate(url)
	w.Run()
	return Closed
}

// OpenBrowser launches the default browser at url.
func OpenBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open browser automatically (%v); open %s manually", err, url)
	}
}
