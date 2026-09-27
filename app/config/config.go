// Package config loads Coralline's configuration. Every field is optional;
// omitting the file entirely yields a working registry (PRD principle:
// operational simplicity is the feature).
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen string `yaml:"listen"`

	Storage struct {
		Backend string `yaml:"backend"` // "filesystem" (only backend in this prototype)
		Path    string `yaml:"path"`
	} `yaml:"storage"`

	Index struct {
		Path string `yaml:"path"`
	} `yaml:"index"`

	Auth struct {
		// RequireLogin puts the browse UI behind sign-in and registry reads
		// behind a bearer token. Publishing always requires a token. An old
		// config still carrying public_read is ignored and lands on the
		// default, so it fails closed.
		RequireLogin   bool     `yaml:"require_login"`
		TrustedProxies []string `yaml:"trusted_proxies"`
	} `yaml:"auth"`

	Limits struct {
		MaxUploadBytes int64 `yaml:"max_upload_bytes"`
	} `yaml:"limits"`

	Log struct {
		Level string `yaml:"level"`
	} `yaml:"log"`

	TLS struct {
		CertFile string `yaml:"cert_file"`
		KeyFile  string `yaml:"key_file"`
		Disabled bool   `yaml:"disabled"` // local evaluation only — see PRD SR-4.3
	} `yaml:"tls"`

	// AdminInternalAddr is where the admin JSON API listens for loopback-only
	// CLI calls (see PRD FR-5.8 — CLI mutating commands go through this API
	// rather than opening the SQLite file directly, to avoid inter-process
	// SQLITE_BUSY contention).
	AdminInternalAddr string `yaml:"admin_internal_addr"`
}

func Default() *Config {
	c := &Config{}
	c.Listen = ":8443"
	c.Storage.Backend = "filesystem"
	c.Storage.Path = "./data/blobs"
	c.Index.Path = "./data/index.db"
	c.Auth.RequireLogin = true
	c.Limits.MaxUploadBytes = 100 * 1024 * 1024
	c.Log.Level = "info"
	c.AdminInternalAddr = "127.0.0.1:8444"
	return c
}

// Load reads a YAML config file if present, overlaying it on defaults.
// A missing file is not an error — it's the zero-config path.
func Load(path string) (*Config, error) {
	c := Default()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}
	return c, nil
}
