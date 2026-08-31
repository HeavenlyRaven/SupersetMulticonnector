package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, date, and platform",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("fedctl %s (commit %s, built %s) %s/%s\n",
				version, commit, date, runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
