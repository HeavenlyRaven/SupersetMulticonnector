package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/ddl"
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run a connectivity probe per source and print a status table",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(false)
			if err != nil {
				return err
			}
			conn, err := openHub(ctx, app)
			if err != nil {
				printCLIError(err)
				return err
			}
			defer conn.Close()

			reader, err := openBIReadOnly(ctx, app)
			if err != nil {
				printCLIError(err)
				return err
			}
			defer reader.Close()

			list, err := conn.List(ctx)
			if err != nil {
				printCLIError(err)
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tTYPE\tTABLES\tSTATUS")
			anyFailed := false
			for _, s := range list {
				status := "ok"
				if err := conn.Probe(ctx, reader, ddl.DatabaseName(s.Name)); err != nil {
					status = "FAILED: " + err.Error()
					anyFailed = true
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", s.Name, s.Type, s.TableCount, status)
			}
			w.Flush()
			if anyFailed {
				return fmt.Errorf("one or more sources failed their connectivity probe")
			}
			return nil
		},
	}
	return cmd
}
