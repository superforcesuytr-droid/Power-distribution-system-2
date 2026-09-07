// Package logging sets up a logger that writes both to stderr (useful when run
// from a terminal) and to a rotating-ish log file (essential for a windowsgui
// executable, whose stderr is discarded).
package logging

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

const maxLogBytes = 2 << 20 // 2 MiB before we start a fresh file

// Setup configures the standard logger. It returns a closer for the file.
func Setup(dir string) (io.Closer, string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.SetOutput(os.Stderr)
		return io.NopCloser(nil), ""
	}
	path := filepath.Join(dir, "power-distribution.log")
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.SetOutput(os.Stderr)
		return io.NopCloser(nil), ""
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	return f, path
}
