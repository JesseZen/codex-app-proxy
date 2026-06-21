package config

import "os"

const defaultLogDir = "~/.codex-proxy/logs"

type Config struct {
	Defaults  Defaults                   `yaml:"defaults"`
	Workers   map[string]WorkerConfig    `yaml:"workers"`
	Providers map[string]ProviderProfile `yaml:"providers"`
}

type Defaults struct {
	LogDir string `yaml:"log_dir"`
}

type WorkerConfig struct {
	Role     string                  `yaml:"role,omitempty" json:"role,omitempty"`
	Port     int                     `yaml:"port"`
	Provider string                  `yaml:"provider"`
	LogLevel string                  `yaml:"log_level,omitempty" json:"log_level,omitempty"`
	Modules  map[string]ModuleConfig `yaml:"modules"`
}

type ModuleConfig struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Params  map[string]any `yaml:",inline" json:"params,omitempty"`
}

type ProviderProfile struct {
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKey    string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	APIFormat string `yaml:"api_format,omitempty" json:"api_format,omitempty"`
}

func (c *Config) ApplyDefaults() {
	if c.Defaults.LogDir == "" {
		c.Defaults.LogDir = defaultLogDir
	}
	if c.Workers == nil {
		c.Workers = map[string]WorkerConfig{}
	}
	if c.Providers == nil {
		c.Providers = map[string]ProviderProfile{}
	}
	for name, worker := range c.Workers {
		if worker.Role == "" {
			worker.Role = "cli"
		}
		if worker.LogLevel == "" {
			worker.LogLevel = "simple"
		}
		if worker.Modules == nil {
			worker.Modules = map[string]ModuleConfig{}
		}
		c.Workers[name] = worker
	}
}

func defaultDirMode() os.FileMode {
	return 0700
}
