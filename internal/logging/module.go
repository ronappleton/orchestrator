package logging

import (
    "go.uber.org/fx"
    "go.uber.org/zap"
)

func Module() fx.Option {
    return fx.Provide(NewLogger)
}

func NewLogger() (*zap.Logger, error) {
    logger, err := zap.NewProduction()
    if err != nil {
        return zap.NewNop(), nil
    }
    return logger, nil
}
