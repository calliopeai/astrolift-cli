package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/astrolift/astrolift-cli/internal/auth"
	"gopkg.in/yaml.v3"
)

// Dir returns the path to the astrolift config directory.
// Defaults to ~/.config/astrolift.
func Dir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "astrolift")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".astrolift")
	}
	return filepath.Join(home, ".config", "astrolift")
}

// ServerEntry describes a single Astrolift server in the config file.
type ServerEntry struct {
	APIURL      string `yaml:"api_url"`
	DisplayName string `yaml:"display_name"`
	IsSaaS      bool   `yaml:"is_saas"`
	CAFile      string `yaml:"ca_file,omitempty"`
}

// Config is the top-level configuration persisted in config.yaml.
type Config struct {
	CurrentServer  string                 `yaml:"current_server"`
	Servers        map[string]ServerEntry `yaml:"servers"`
	DefaultOrg     string                 `yaml:"default_org,omitempty"`
	DefaultProject string                 `yaml:"default_project,omitempty"`
	Output         string                 `yaml:"output,omitempty"`
	LogLevel       string                 `yaml:"log_level,omitempty"`
}

// Load reads the config file from disk. Returns a zero-value Config if
// the file does not exist.
func Load() (*Config, error) {
	path := filepath.Join(Dir(), "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				Servers: make(map[string]ServerEntry),
				Output:  "human",
			}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]ServerEntry)
	}
	return &cfg, nil
}

// Save writes the config to disk, creating the directory if needed.
func (c *Config) Save() error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	path := filepath.Join(dir, "config.yaml")
	return os.WriteFile(path, data, 0o644)
}

// LoadCredentials reads the credentials file for the given server slug.
// The file must be mode 0600; the function refuses to read it otherwise.
func LoadCredentials(serverSlug string) (*auth.Credentials, error) {
	path := filepath.Join(Dir(), "credentials", serverSlug+".yaml")

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}

	mode := info.Mode().Perm()
	if mode != 0o600 {
		return nil, fmt.Errorf("credentials file %s has mode %o; expected 0600", path, mode)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}

	var creds auth.Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	return &creds, nil
}

// DeleteCredentials removes the stored credentials file for a server.
// No-op when the file doesn't exist.
func DeleteCredentials(serverSlug string) error {
	path := filepath.Join(Dir(), "credentials", serverSlug+".yaml")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing credentials: %w", err)
	}
	return nil
}

// SaveCredentials writes credentials for a server slug, creating the
// credentials directory and setting mode 0600 on the file.
func SaveCredentials(serverSlug string, creds *auth.Credentials) error {
	dir := filepath.Join(Dir(), "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating credentials dir: %w", err)
	}

	data, err := yaml.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshalling credentials: %w", err)
	}

	path := filepath.Join(dir, serverSlug+".yaml")
	return os.WriteFile(path, data, 0o600)
}
