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

// Open shows the UI on macOS. It prefers an app-mode window from an installed
// Chromium browser and blocks until that window is closed, returning true. With
// no such browser it falls back to the default browser plus a small "running"
// dialog that gives the operator a way to quit; if even that is unavailable it
// returns false so the caller keeps serving until interrupted.
func Open(url, title, dataPath string, width, height int) bool {
	if bin := findAppModeBrowser(); bin != "" {
		// A dedicated profile directory matters: with the user's normal profile
		// the new process would hand the URL to the already-running browser and
		// exit immediately, and we would quit before showing anything.
		cmd := exec.Command(bin,
			"--app="+url,
			"--user-data-dir="+dataPath,
			fmt.Sprintf("--window-size=%d,%d", width, height),
			"--no-first-run",
			"--no-default-browser-check",
		)
		if err := cmd.Start(); err == nil {
			log.Printf("opened app window using %s", filepath.Base(bin))
			_ = cmd.Wait()
			return true
		} else {
			log.Printf("could not start %s (%v); falling back to the default browser", bin, err)
		}
	}
	OpenBrowser(url)
	return quitDialog(title, url)
}

// quitDialog blocks on a stock macOS dialog so the operator can stop the
// application once they are finished with the browser tab. It reports whether
// the dialog was actually shown.
func quitDialog(title, url string) bool {
	script := fmt.Sprintf(
		`display dialog %q with title %q buttons {"Quit"} default button "Quit" with icon note`,
		title+" is running.\n\nThe interface is open in your browser at "+url+
			"\n\nLeave this dialog open while you work, then click Quit to stop the application.",
		title)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		log.Printf("running in browser mode; press Ctrl+C to stop (%v)", err)
		return false
	}
	return true
}

// OpenBrowser launches the default browser at url.
func OpenBrowser(url string) {
	if err := exec.Command("open", url).Start(); err != nil {
		log.Printf("could not open browser automatically (%v); open %s manually", err, url)
	}
}
