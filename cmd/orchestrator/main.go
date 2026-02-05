package main

import (
	"os"

	"github.com/ronappleton/orchestrator/internal/cli"
	"github.com/ronappleton/orchestrator/internal/config"
	"github.com/ronappleton/orchestrator/internal/httpserver"
	"github.com/ronappleton/orchestrator/internal/logging"
	"github.com/ronappleton/orchestrator/internal/metrics"
	"github.com/ronappleton/orchestrator/internal/workflow"
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
		workflowModule(),
		httpserver.Module(),
	)

	app.Run()
}

func workflowModule() fx.Option {
	return fx.Options(
		fx.Provide(func(cfg config.Config) workflow.Store {
			if cfg.Database.DSN == "" {
				return workflow.NewMemoryStore()
			}
			store, err := workflow.NewPGStore(cfg.Database.DSN)
			if err != nil {
				return workflow.NewMemoryStore()
			}
			return store
		}),
		fx.Provide(func(cfg config.Config) *workflow.Notifier {
			return workflow.NewNotifier(cfg.MemArch.BaseURL, cfg.MemArch.Timeout, cfg.AuditLog.BaseURL, cfg.AuditLog.Timeout)
		}),
		fx.Provide(func(store workflow.Store, notify *workflow.Notifier, cfg config.Config) *workflow.Engine {
			eng := workflow.NewEngine(store, notify)
			eng.SetPolicyRequiresApproval(cfg.Policy.RequireApproval)
			return eng
		}),
		fx.Provide(func(store workflow.Store, engine *workflow.Engine) *workflow.Service {
			return workflow.NewService(store, engine)
		}),
	)
}
