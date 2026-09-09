// Package instance keeps a single copy of the application serving at a time.
//
// The running copy records its URL in a small file next to the configuration.
// A second launch finds that file, checks the URL really answers, and shows
// that window instead of starting a rival server on another port.
package instance

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type info struct {
	PID     int       `json:"pid"`
	URL     string    `json:"url"`
	Started time.Time `json:"started"`
}

func lockPath(dir string) string { return filepath.Join(dir, "instance.json") }

// Existing returns the URL of a copy of this same build that is already
// serving, if any. A file left behind by a crashed or killed process is
// ignored, because the URL is probed rather than trusted, and so is a copy
// reporting a different version: after an update, opening the application must
// show the version just installed rather than an older one that happens to
// still be running.
func Existing(dir, version string) (string, bool) {
	data, err := os.ReadFile(lockPath(dir))
	if err != nil {
		return "", false
	}
	var i info
	if err := json.Unmarshal(data, &i); err != nil || i.URL == "" {
		return "", false
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(strings.TrimSuffix(i.URL, "/") + "/api/status")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	// Confirm it is one of ours and not an unrelated service that took the port.
	var probe struct {
		Version string `json:"version"`
		DB      *struct {
			State string `json:"state"`
		} `json:"db"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&probe); err != nil || probe.DB == nil {
		return "", false
	}
	if probe.Version != version {
		return "", false
	}
	return i.URL, true
}

// Acquire records this process as the running copy and returns a release
// function to call on shutdown.
func Acquire(dir, url string) func() {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	data, err := json.Marshal(info{PID: os.Getpid(), URL: url, Started: time.Now()})
	if err != nil {
		return func() {}
	}
	if err := os.WriteFile(lockPath(dir), data, 0o600); err != nil {
		return func() {}
	}
	return func() { _ = os.Remove(lockPath(dir)) }
}
