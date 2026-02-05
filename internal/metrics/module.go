package metrics

import "go.uber.org/fx"

type Metrics struct {}

func Module() fx.Option {
    return fx.Provide(New)
}

func New() *Metrics {
    return &Metrics{}
}
