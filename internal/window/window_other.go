//go:build !windows && !darwin

package window

import (
	"log"
	"os/exec"
)

// Open shows the UI in the default browser, whose lifetime is not ours to
// observe, so the caller watches the page instead.
func Open(url, title, dataPath string, width, height int) Mode {
	OpenBrowser(url)
	return Handed
}

// OpenBrowser launches the system browser at url.
func OpenBrowser(url string) {
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		log.Printf("could not open browser automatically (%v); open %s manually", err, url)
	}
}

// Reveal brings the interface of a copy that is already serving back on screen.
func Reveal(url, title, dataPath string, width, height int) {
	OpenBrowser(url)
}
