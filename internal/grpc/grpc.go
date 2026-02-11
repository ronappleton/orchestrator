package grpc

import (
	"net"
	"strconv"

	"github.com/ronappleton/orchestrator/internal/config"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func NewServer(log *zap.Logger, cfg config.Config) *grpc.Server {
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(otelgrpc.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(otelgrpc.StreamServerInterceptor()),
	}
	srv := grpc.NewServer(opts...)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	log.Info("grpc health enabled")
	return srv
}

func NewListener(cfg config.Config) (net.Listener, error) {
	addr := net.JoinHostPort(cfg.GRPC.Host, strconv.Itoa(cfg.GRPC.Port))
	return net.Listen("tcp", addr)
}
