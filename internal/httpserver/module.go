package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ronappleton/orchestrator/internal/config"
	"github.com/ronappleton/orchestrator/internal/workflow"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Server struct {
	cfg    config.Config
	logger *zap.Logger
	wf     *workflow.Service
	srv    *http.Server
}

func Module() fx.Option {
	return fx.Options(
		fx.Provide(NewServer),
		fx.Invoke(RegisterHooks),
	)
}

func NewServer(cfg config.Config, logger *zap.Logger, wf *workflow.Service) *Server {
	mux := http.NewServeMux()
	s := &Server{cfg: cfg, logger: logger, wf: wf}

	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/docs", s.handleDocs)
	mux.HandleFunc("/v1/workflows", s.handleWorkflows)
	mux.HandleFunc("/v1/workflows/", s.handleWorkflowVersionRoutes)
	mux.HandleFunc("/v1/templates", s.handleTemplates)
	mux.HandleFunc("/v1/runs", s.handleRuns)
	mux.HandleFunc("/v1/runs/", s.handleRunByID)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.srv = srv
	return s
}

func RegisterHooks(lc fx.Lifecycle, server *Server) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			server.logger.Info("http server starting", zap.String("addr", server.srv.Addr))
			go func() {
				if err := server.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					server.logger.Error("http server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			server.logger.Info("http server stopping")
			return server.srv.Shutdown(shutdownCtx)
		},
	})
}
