//go:build !windows

package window

import (
	"log"
	"os/exec"
	"runtime"
)

// Open shows the UI. On non-Windows platforms there is no embedded WebView2,
// so the default browser is used and Open returns false to signal that the
// caller should keep the server alive until interrupted.
func Open(url, title, dataPath string, width, height int) bool {
	OpenBrowser(url)
	return false
}

// OpenBrowser launches the system browser at url.
func OpenBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open browser automatically (%v); open %s manually", err, url)
	}
}
