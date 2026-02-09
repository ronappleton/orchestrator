package httpserver

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "github.com/ronappleton/orchestrator/internal/config"
    "go.uber.org/fx"
    "go.uber.org/zap"
)

type Server struct {
    cfg    config.Config
    logger *zap.Logger
    srv    *http.Server
}

func Module() fx.Option {
    return fx.Options(
        fx.Provide(NewServer),
        fx.Invoke(RegisterHooks),
    )
}

func NewServer(cfg config.Config, logger *zap.Logger) *Server {
    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.Header().Set("content-type", "application/json")
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte(`{"status":"ok"}`))
    })
    mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
        w.Header().Set("content-type", "text/plain; charset=utf-8")
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("# metrics placeholder\n"))
    })
    mux.HandleFunc("/docs", func(w http.ResponseWriter, _ *http.Request) {
        w.Header().Set("content-type", "text/plain; charset=utf-8")
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("Docs not yet available"))
    })

    addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
    srv := &http.Server{
        Addr:              addr,
        Handler:           mux,
        ReadHeaderTimeout: 5 * time.Second,
    }

    return &Server{cfg: cfg, logger: logger, srv: srv}
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
