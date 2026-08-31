package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	var volumes, dev bool
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Tear down the stack",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := dockerAvailable(ctx); err != nil {
				printCLIError(err)
				return err
			}
			downArgs := runComposeArgs(dev, "down")
			if volumes {
				downArgs = append(downArgs, "--volumes")
			}
			if err := runDockerCompose(ctx, downArgs); err != nil {
				printCLIError(fmt.Errorf("docker compose down failed: %w", err))
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&volumes, "volumes", false, "also remove the named volumes (ch-data, ch-user-files, superset-home)")
	cmd.Flags().BoolVar(&dev, "dev", false, "also tear down compose.dev.yaml's sample source databases")
	return cmd
}
