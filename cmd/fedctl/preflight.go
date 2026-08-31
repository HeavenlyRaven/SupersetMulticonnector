package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/preflight"
)

// pinnedImages mirrors the images actually referenced by compose.yaml —
// see that file and images/*/Dockerfile for the source of truth.
func pinnedImages() []preflight.PinnedImage {
	return []preflight.PinnedImage{
		{Name: "superset-base", Ref: SupersetBaseImage},
		{Name: "clickhouse-base", Ref: ClickHouseBaseImage},
	}
}

func newPreflightCmd() *cobra.Command {
	var skipImages bool
	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Check the host environment before `fedctl up`",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := preflight.Options{
				MetadataURI: os.Getenv("SQLALCHEMY_DATABASE_URI"),
			}
			if !skipImages {
				opts.PinnedImages = pinnedImages()
			}
			results, ok := preflight.Run(cmd.Context(), opts)
			printPreflightResults(results)

			if !ok {
				return fmt.Errorf("preflight failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&skipImages, "skip-images", false, "skip the registry manifest checks (useful offline)")
	return cmd
}

// printPreflightResults renders results as a table — shared by `preflight`
// and `install`, which runs the same checks as its second step.
func printPreflightResults(results []preflight.Result) {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "CHECK\tSTATUS\tMESSAGE")
	for _, r := range results {
		status := "ok"
		if !r.OK {
			status = "FAIL"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Name, status, r.Message)
		if !r.OK && r.Hint != "" {
			fmt.Fprintf(w, "\t\thint: %s\n", r.Hint)
		}
	}
	w.Flush()
}
