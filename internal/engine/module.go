package engine

import (
	"context"

	"go.uber.org/fx"
)

func Module() fx.Option {
	return fx.Options(
		fx.Provide(NewService),
		fx.Invoke(func(lc fx.Lifecycle, svc *Service) {
			lc.Append(fx.Hook{OnStop: func(ctx context.Context) error {
				svc.Close()
				return nil
			}})
		}),
	)
}
