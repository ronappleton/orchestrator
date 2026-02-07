package main

import (
	"os"

	"context"

	"github.com/ronappleton/orchestrator/internal/cli"
	"github.com/ronappleton/orchestrator/internal/config"
	"github.com/ronappleton/orchestrator/internal/logging"
	"github.com/ronappleton/orchestrator/internal/metrics"
	"github.com/ronappleton/orchestrator/internal/otel"
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
	shutdown, err := otel.Init("orchestrator")
	if err == nil {
		defer shutdown(context.Background())
	}

	app := fx.New(
		config.Module(configPath),
		logging.Module(),
		metrics.Module(),
		workflowModule(),
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
			return workflow.NewNotifier(
				cfg.MemArch.BaseURL, cfg.MemArch.GRPCAddress, cfg.MemArch.Timeout,
				cfg.AuditLog.BaseURL, cfg.AuditLog.GRPCAddress, cfg.AuditLog.Timeout,
				cfg.EventBus.BaseURL, cfg.EventBus.GRPCAddress, cfg.EventBus.Timeout,
			)
		}),
		fx.Provide(func(store workflow.Store, notify *workflow.Notifier, cfg config.Config) *workflow.Engine {
			eng := workflow.NewEngine(
				store,
				notify,
				cfg.Notification.BaseURL,
				cfg.Notification.GRPCAddress,
				cfg.Notification.Timeout,
				cfg.Workspace.BaseURL,
				cfg.Workspace.GRPCAddress,
				cfg.Workspace.Timeout,
				cfg.Policy.BaseURL,
				cfg.Policy.GRPCAddress,
				cfg.Policy.Timeout,
			)
			eng.SetPolicyRequiresApproval(cfg.Policy.RequireApproval)
			return eng
		}),
		fx.Provide(func(store workflow.Store, engine *workflow.Engine) *workflow.Service {
			return workflow.NewService(store, engine)
		}),
	)
}
