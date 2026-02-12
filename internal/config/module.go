package config

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"go.uber.org/fx"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Database     DatabaseConfig `yaml:"database"`
	MemArch      EndpointConfig `yaml:"memarch"`
	AuditLog     EndpointConfig `yaml:"audit_log"`
	Notification EndpointConfig `yaml:"notification"`
	Workspace    EndpointConfig `yaml:"workspace"`
	EventBus     EndpointConfig `yaml:"event_bus"`
	Approval     EndpointConfig `yaml:"approval"`
	GRPC         GRPCConfig     `yaml:"grpc"`
	Policy       PolicyConfig   `yaml:"policy"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"`
}

type EndpointConfig struct {
	BaseURL     string `yaml:"base_url"`
	GRPCAddress string `yaml:"grpc_address"`
	Timeout     string `yaml:"timeout"`
}

type GRPCConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type PolicyConfig struct {
	RequireApproval bool   `yaml:"require_approval"`
	WorkflowSchema  string `yaml:"workflow_schema"`
	BaseURL         string `yaml:"base_url"`
	GRPCAddress     string `yaml:"grpc_address"`
	Timeout         string `yaml:"timeout"`
}

func Default() Config {
	return Config{
		Database: DatabaseConfig{
			DSN: "",
		},
		MemArch: EndpointConfig{
			BaseURL:     "",
			GRPCAddress: "",
			Timeout:     "5s",
		},
		AuditLog: EndpointConfig{
			BaseURL:     "",
			GRPCAddress: "",
			Timeout:     "5s",
		},
		Notification: EndpointConfig{
			BaseURL:     "",
			GRPCAddress: "",
			Timeout:     "5s",
		},
		Workspace: EndpointConfig{
			BaseURL:     "",
			GRPCAddress: "",
			Timeout:     "5s",
		},
		EventBus: EndpointConfig{
			BaseURL:     "",
			GRPCAddress: "",
			Timeout:     "5s",
		},
		Approval: EndpointConfig{
			BaseURL:     "",
			GRPCAddress: "",
			Timeout:     "5s",
		},
		GRPC: GRPCConfig{
			Host: "0.0.0.0",
			Port: 9114,
		},
		Policy: PolicyConfig{
			RequireApproval: false,
			WorkflowSchema:  "",
			BaseURL:         "",
			GRPCAddress:     "",
			Timeout:         "5s",
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
	if v := strings.TrimSpace(os.Getenv("APP_GRPC_HOST")); v != "" {
		cfg.GRPC.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("APP_GRPC_PORT")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.GRPC.Port = parsed
		}
	}
	return cfg, nil
}

func Module(path string) fx.Option {
	return fx.Provide(func() (Config, error) {
		return Load(path)
	})
}
