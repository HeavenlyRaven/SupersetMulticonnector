package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// seedNameRe constrains the destination filename inside ch-user-files —
// it becomes part of a docker CLI argument, not a shell string, but stays
// strict regardless: no path separators, no traversal.
var seedNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

func newSeedCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "seed", Short: "Seed data into the stack's volumes"}
	cmd.AddCommand(newSeedSQLiteCmd())
	return cmd
}

func newSeedSQLiteCmd() *cobra.Command {
	var from, as string
	cmd := &cobra.Command{
		Use:   "sqlite",
		Short: "Copy a host SQLite file into the ch-user-files volume",
		Long: "Copies a host SQLite file into the ch-user-files named volume via a throwaway\n" +
			"container, so the running stack never needs a bind mount for source data.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				err := fmt.Errorf("--from is required")
				printCLIError(err)
				return err
			}
			info, err := os.Stat(from)
			if err != nil {
				printCLIError(fmt.Errorf("cannot stat %q: %w", from, err))
				return err
			}
			if !info.Mode().IsRegular() {
				err := fmt.Errorf("%q is not a regular file", from)
				printCLIError(err)
				return err
			}
			dest := as
			if dest == "" {
				dest = filepath.Base(from)
			}
			if !seedNameRe.MatchString(dest) {
				err := fmt.Errorf("invalid destination name %q: must match %s", dest, seedNameRe.String())
				printCLIError(err)
				return err
			}

			absFrom, err := filepath.Abs(from)
			if err != nil {
				printCLIError(err)
				return err
			}
			hostDir := filepath.ToSlash(filepath.Dir(absFrom))
			base := filepath.Base(absFrom)

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			// Live-verified: a bare `-v ch-user-files:/dest` here creates
			// or references an unprefixed volume literally named
			// "ch-user-files" — a completely different volume from the
			// one Compose actually mounts into clickhouse/superset,
			// which it names using the project name (e.g.
			// "superset-federation_ch-user-files"). Seeding silently
			// "succeeded" into a volume nothing ever reads. Resolve the
			// real volume via Compose's own labels instead of guessing.
			volume, err := resolveComposeVolume(ctx, "ch-user-files")
			if err != nil {
				printCLIError(err)
				return err
			}

			dockerArgs := []string{
				"run", "--rm",
				"-v", hostDir + ":/src:ro,z",
				"-v", volume + ":/dest",
				ClickHouseBaseImage,
				"cp", "/src/" + base, "/dest/" + dest,
			}
			out, err := exec.CommandContext(ctx, "docker", dockerArgs...).CombinedOutput()
			if err != nil {
				printCLIError(fmt.Errorf("seeding %q into ch-user-files failed: %w: %s", dest, err, strings.TrimSpace(string(out))))
				return err
			}
			fmt.Printf("seeded %s -> ch-user-files/%s\n", from, dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "path to the host SQLite file")
	cmd.Flags().StringVar(&as, "as", "", "destination filename inside ch-user-files (default: the source basename)")
	return cmd
}
