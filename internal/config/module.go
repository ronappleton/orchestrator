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
	Server   ServerConfig   `yaml:"server"`
	GRPC     GRPCConfig     `yaml:"grpc"`
	Policy   EndpointConfig `yaml:"policy"`
	Approval EndpointConfig `yaml:"approval"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type GRPCConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type EndpointConfig struct {
	GRPCAddress string `yaml:"grpc_address"`
	Timeout     string `yaml:"timeout"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8100,
		},
		GRPC: GRPCConfig{
			Host: "0.0.0.0",
			Port: 9114,
		},
		Policy: EndpointConfig{
			GRPCAddress: "policy-service:9103",
			Timeout:     "5s",
		},
		Approval: EndpointConfig{
			GRPCAddress: "approval-service:9113",
			Timeout:     "5s",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return cfg, err
			}
		} else if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
	}

	if v := strings.TrimSpace(os.Getenv("APP_GRPC_HOST")); v != "" {
		cfg.GRPC.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("APP_GRPC_PORT")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			cfg.GRPC.Port = parsed
		}
	}
	if v := strings.TrimSpace(os.Getenv("APP_POLICY_GRPC_ADDRESS")); v != "" {
		cfg.Policy.GRPCAddress = v
	}
	if v := strings.TrimSpace(os.Getenv("APP_APPROVAL_GRPC_ADDRESS")); v != "" {
		cfg.Approval.GRPCAddress = v
	}
	if v := strings.TrimSpace(os.Getenv("APP_POLICY_TIMEOUT")); v != "" {
		cfg.Policy.Timeout = v
	}
	if v := strings.TrimSpace(os.Getenv("APP_APPROVAL_TIMEOUT")); v != "" {
		cfg.Approval.Timeout = v
	}

	return cfg, nil
}

func Module(path string) fx.Option {
	return fx.Provide(func() (Config, error) {
		return Load(path)
	})
}
