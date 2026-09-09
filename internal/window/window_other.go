//go:build !windows && !darwin

package window

import (
	"log"
	"os/exec"
)

// Open shows the UI. On these platforms there is no embedded webview, so the
// default browser is used and Open returns false to signal that the caller
// should keep the server alive until interrupted.
func Open(url, title, dataPath string, width, height int) bool {
	OpenBrowser(url)
	return false
}

// OpenBrowser launches the system browser at url.
func OpenBrowser(url string) {
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		log.Printf("could not open browser automatically (%v); open %s manually", err, url)
	}
}
