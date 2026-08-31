package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/health"
)

func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <target>",
		Short: "Run a healthcheck probe: clickhouse or superset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := loadApp(false)
			if err != nil {
				return err
			}
			if err := health.Check(cmd.Context(), args[0], app); err != nil {
				printCLIError(err)
				return err
			}
			fmt.Println(args[0] + ": healthy")
			return nil
		},
	}
	cmd.ValidArgs = health.ValidTargets
	return cmd
}
