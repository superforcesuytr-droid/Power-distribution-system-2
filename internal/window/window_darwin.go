//go:build darwin

package window

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Chromium-based browsers, in preference order. Any of them can host the UI in
// "app mode": a plain window with no tabs, address bar or browser chrome, which
// is as close to a native window as we can get without cgo and the macOS SDK.
var appModeBrowsers = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
}

func findAppModeBrowser() string {
	candidates := append([]string{}, appModeBrowsers...)
	if home, err := os.UserHomeDir(); err == nil {
		for _, p := range appModeBrowsers {
			candidates = append(candidates, filepath.Join(home, strings.TrimPrefix(p, "/")))
		}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Open shows the UI on macOS in an app-mode window from an installed Chromium
// browser, falling back to the default browser.
//
// It deliberately does not wait for the browser process. When a browser is
// already running with the same profile, the process launched here passes the
// address to that running copy and exits within milliseconds; waiting on it
// would look exactly like the operator closing the window, and the application
// would shut down the moment it had started. The window's real lifetime is
// tracked by the page itself, so Open reports Handed and leaves that to the
// caller.
func Open(url, title, dataPath string, width, height int) Mode {
	if openAppWindow(url, dataPath, width, height) {
		return Handed
	}
	OpenBrowser(url)
	return Handed
}

// Reveal brings the interface of a copy that is already serving back on
// screen. It uses the same app-mode window as a first launch, so reopening the
// application looks like the application rather than like a browser tab.
func Reveal(url, title, dataPath string, width, height int) {
	if !openAppWindow(url, dataPath, width, height) {
		OpenBrowser(url)
	}
}

// openAppWindow asks an installed Chromium browser for a chrome-less window and
// reports whether one was launched.
func openAppWindow(url, dataPath string, width, height int) bool {
	bin := findAppModeBrowser()
	if bin == "" {
		return false
	}
	// A dedicated profile directory matters: with the user's normal profile the
	// new process would hand the URL to the browser they already have open, and
	// it would arrive as an ordinary tab rather than a window of our own.
	cmd := exec.Command(bin,
		"--app="+url,
		"--user-data-dir="+dataPath,
		fmt.Sprintf("--window-size=%d,%d", width, height),
		"--no-first-run",
		"--no-default-browser-check",
	)
	if err := cmd.Start(); err != nil {
		log.Printf("could not start %s (%v); falling back to the default browser", bin, err)
		return false
	}
	// Reap the process when it exits so it does not linger as a zombie.
	go func() { _ = cmd.Wait() }()
	log.Printf("opened app window using %s", filepath.Base(bin))
	return true
}

// OpenBrowser launches the default browser at url.
func OpenBrowser(url string) {
	if err := exec.Command("open", url).Start(); err != nil {
		log.Printf("could not open browser automatically (%v); open %s manually", err, url)
	}
}
