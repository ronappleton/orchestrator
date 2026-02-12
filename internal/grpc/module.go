package grpc

import (
	"context"
	"net"

	orchestratorpb "github.com/ronappleton/orchestrator/pkg/pb"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

var Module = fx.Options(
	fx.Provide(
		NewServer,
		NewListener,
		fx.Annotate(
			NewOrchestratorService,
			fx.As(new(orchestratorpb.OrchestratorServiceServer)),
		),
	),
	fx.Invoke(
		registerService,
		lifecycleHook,
	),
)

type registerParams struct {
	fx.In

	Server  *grpc.Server
	Service orchestratorpb.OrchestratorServiceServer
}

func registerService(p registerParams) {
	orchestratorpb.RegisterOrchestratorServiceServer(p.Server, p.Service)
}

func lifecycleHook(lc fx.Lifecycle, log *zap.Logger, srv *grpc.Server, lis net.Listener) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			log.Info("grpc server starting", zap.String("addr", lis.Addr().String()))
			go func() {
				if err := srv.Serve(lis); err != nil {
					log.Error("grpc server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			log.Info("grpc server stopping")
			srv.GracefulStop()
			return nil
		},
	})
}
