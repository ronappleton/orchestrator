package main

import (
    "os"

    "github.com/ronappleton/orchestrator/internal/cli"
    "github.com/ronappleton/orchestrator/internal/config"
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
        httpserver.Module(),
    )

    app.Run()
}
