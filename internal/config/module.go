package config

import (
    "errors"
    "os"

    "go.uber.org/fx"
    "gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	MemArch  EndpointConfig `yaml:"memarch"`
	AuditLog EndpointConfig `yaml:"audit_log"`
	Policy PolicyConfig `yaml:"policy"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type EndpointConfig struct {
	BaseURL string `yaml:"base_url"`
	Timeout string `yaml:"timeout"`
}

type PolicyConfig struct {
	RequireApproval bool `yaml:"require_approval"`
	WorkflowSchema  string `yaml:"workflow_schema"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8100,
		},
		Database: DatabaseConfig{
			DSN: "",
		},
		MemArch: EndpointConfig{
			BaseURL: "",
			Timeout: "5s",
		},
		AuditLog: EndpointConfig{
			BaseURL: "",
			Timeout: "5s",
		},
		Policy: PolicyConfig{
			RequireApproval: false,
			WorkflowSchema:  "",
		},
	}
}

func Load(path string) (Config, error) {
    cfg := Default()
    if path == "" {
        return cfg, nil
    }

    data, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return cfg, nil
        }
        return cfg, err
    }

    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return cfg, err
    }
    return cfg, nil
}

func Module(path string) fx.Option {
    return fx.Provide(func() (Config, error) {
        return Load(path)
    })
}
