package config

import (
    "errors"
    "os"

    "go.uber.org/fx"
    "gopkg.in/yaml.v3"
)

type Config struct {
    Server ServerConfig `yaml:"server"`
}

type ServerConfig struct {
    Host string `yaml:"host"`
    Port int    `yaml:"port"`
}

func Default() Config {
    return Config{
        Server: ServerConfig{
            Host: "0.0.0.0",
            Port: 8100,
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
