package cli

import "github.com/spf13/cobra"

func NewRootCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "service",
        Short: "Service runner",
    }

    cmd.Flags().String("config", "config.yaml", "Path to config file")
    return cmd
}
