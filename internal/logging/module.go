package logging

import (
    "github.com/ronappleton/ai-eco-system/pkg/logging"
    "go.uber.org/fx"
)

func Module() fx.Option {
    return logging.Module("orchestrator")
}
