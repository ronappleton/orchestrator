package main

import (
	"os"

	"github.com/ronappleton/orchestrator/internal/cli"
	"github.com/ronappleton/orchestrator/internal/config"
	"github.com/ronappleton/orchestrator/internal/discoveryannounce"
	"github.com/ronappleton/orchestrator/internal/engine"
	grpcserver "github.com/ronappleton/orchestrator/internal/grpc"
	"github.com/ronappleton/orchestrator/internal/httpserver"
	"github.com/ronappleton/orchestrator/internal/logging"
	"github.com/ronappleton/orchestrator/internal/metrics"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func main() {
	rootCmd := cli.NewRootCommand()

	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config")
		startServer(configPath)
		return nil
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func startServer(configPath string) {
	app := fx.New(
		config.Module(configPath),
		logging.Module(),
		metrics.Module(),
		engine.Module(),
		grpcserver.Module,
		httpserver.Module(),
		fx.Invoke(func(params discoveryannounce.Params, cfg config.Config) {
			discoveryannounce.Register(params, "orchestrator", cfg.GRPC.Host, cfg.GRPC.Port)
		}),
	)

	app.Run()
}
