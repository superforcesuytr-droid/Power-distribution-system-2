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
// the window is closed and returns true. If WebView2 is unavailable it falls
// back to the default browser and returns false so the caller keeps serving.
func Open(url, title, dataPath string, width, height int) bool {
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
		return false
	}
	defer w.Destroy()
	w.SetSize(width, height, webview2.HintNone)
	w.Navigate(url)
	w.Run()
	return true
}

// OpenBrowser launches the default browser at url.
func OpenBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open browser automatically (%v); open %s manually", err, url)
	}
}
