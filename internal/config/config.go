// Package config loads and saves the application's connection settings.
//
// Resolution order for the config file:
//  1. --config flag / PDS_CONFIG environment variable
//  2. config.json next to the executable (portable mode)
//  3. <user config dir>/PowerDistribution/config.json
//     (e.g. %APPDATA%\PowerDistribution\config.json on Windows)
//
// DATABASE_URL, when set, overrides whatever the file contains.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const appDirName = "PowerDistribution"

// Database holds the PostgreSQL connection parameters entered on the setup screen.
type Database struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Name     string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
}

// Config is what gets persisted to disk.
type Config struct {
	Database   Database `json:"database"`
	WindowMode string   `json:"window_mode,omitempty"` // "native" (default) or "browser"

	path string
}

// Path returns where the config is (or will be) stored.
func (c *Config) Path() string { return c.path }

// Configured reports whether enough is present to attempt a connection.
func (d Database) Configured() bool {
	return d.Host != "" && d.Name != "" && d.User != ""
}

// DSN renders the parameters as a libpq/pgx connection URL.
func (d Database) DSN() string {
	if !d.Configured() {
		return ""
	}
	port := d.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", d.Host, port),
		Path:   "/" + d.Name,
	}
	if d.Password != "" {
		u.User = url.UserPassword(d.User, d.Password)
	} else {
		u.User = url.User(d.User)
	}
	q := url.Values{}
	ssl := d.SSLMode
	if ssl == "" {
		ssl = "prefer"
	}
	q.Set("sslmode", ssl)
	q.Set("application_name", "power-distribution")
	u.RawQuery = q.Encode()
	return u.String()
}

// Redacted returns a copy safe to send to the UI.
func (d Database) Redacted() Database {
	d.Password = ""
	return d
}

// ResolvePath decides which file to use, honouring the override if given.
func ResolvePath(override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv("PDS_CONFIG"); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		portable := filepath.Join(filepath.Dir(exe), "config.json")
		if _, err := os.Stat(portable); err == nil {
			return portable
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	return filepath.Join(dir, appDirName, "config.json")
}

// Dir returns the directory that holds the config file (also used for logs).
func Dir(path string) string { return filepath.Dir(path) }

// Load reads the config file. A missing file yields an empty config, not an error.
func Load(path string) (*Config, error) {
	c := &Config{path: path}
	c.Database.Port = 5432
	c.Database.SSLMode = "prefer"
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// Save writes the config atomically, creating the directory if needed.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// EffectiveDSN returns DATABASE_URL if set, otherwise the saved parameters.
func (c *Config) EffectiveDSN() (dsn string, fromEnv bool) {
	if env := strings.TrimSpace(os.Getenv("DATABASE_URL")); env != "" {
		return env, true
	}
	return c.Database.DSN(), false
}
