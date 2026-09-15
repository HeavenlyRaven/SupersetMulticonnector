package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// testkitComposeFile is resolved relative to the current directory, the
// same convention as compose.yaml itself — fedctl has no notion of an
// install location of its own. It's shipped at this exact path by both
// the Windows installer and the Linux packages, alongside the repo.
const testkitComposeFile = "test/testkit/compose.yaml"

// testkitComposeArgs is the shared prefix every `docker compose` call
// against the testkit needs — deliberately not runComposeArgs, which is
// hardcoded to compose.yaml/compose.dev.yaml and has no notion of a
// second, unrelated compose project.
func testkitComposeArgs(rest ...string) []string {
	return append([]string{"compose", "-f", testkitComposeFile}, rest...)
}

func newTestkitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "testkit",
		Short: "Disposable test databases for exercising a fresh install — never used by the real stack",
		Long: "Starts three throwaway databases in Docker — a Superset metadata Postgres, a\n" +
			"federated Postgres source, and a federated MySQL source, each with real sample\n" +
			"rows — so you can exercise `fedctl install` end to end on a machine that has\n" +
			"nothing else set up yet. Entirely independent of the real compose.yaml: safe to\n" +
			"run before, after, or alongside it, on the same machine or a completely\n" +
			"different one.\n\n" +
			"Not part of the product an end user ever touches — this exists to make testing\n" +
			"a release (the installer, the Linux packages, the published images) as close to\n" +
			"one command as possible. See test/testkit/README.md.",
	}
	cmd.AddCommand(newTestkitUpCmd())
	cmd.AddCommand(newTestkitDownCmd())
	cmd.AddCommand(newTestkitInfoCmd())
	return cmd
}

func newTestkitUpCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the testkit databases and print everything you need to connect to them",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runTestkitUp(cmd.Context(), timeout); err != nil {
				printCLIError(err)
				return err
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "how long to wait for the databases to become healthy")
	return cmd
}

func runTestkitUp(ctx context.Context, timeout time.Duration) error {
	if err := dockerAvailable(ctx); err != nil {
		return err
	}
	fmt.Println("starting testkit databases (metadata-postgres, source-postgres, source-mysql)...")
	if err := runDockerCompose(ctx, testkitComposeArgs("up", "-d")); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	fmt.Println("waiting for them to become healthy...")
	if err := waitComposeHealthy(ctx, testkitComposeArgs(), timeout); err != nil {
		return err
	}
	printTestkitInfo()
	return nil
}

func newTestkitDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the testkit databases and delete all their data",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := dockerAvailable(ctx); err != nil {
				printCLIError(err)
				return err
			}
			if err := runDockerCompose(ctx, testkitComposeArgs("down", "-v")); err != nil {
				err = fmt.Errorf("docker compose down failed: %w", err)
				printCLIError(err)
				return err
			}
			return nil
		},
	}
	return cmd
}

func newTestkitInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Print the testkit connection details again, without starting or checking anything",
		RunE: func(cmd *cobra.Command, args []string) error {
			printTestkitInfo()
			return nil
		},
	}
	return cmd
}

// printTestkitInfo prints every value the testkit's own ports/credentials
// fix in advance — nothing here is discovered at runtime, so `info` can
// reprint it any time without touching Docker at all. This is the whole
// point of the command: one screen with every value `fedctl install` and
// `fedctl source add` will ask for, so there is nothing left to hunt for
// across three different terminals.
func printTestkitInfo() {
	fmt.Println()
	fmt.Println("=== Testkit databases are ready ===")
	fmt.Println()
	fmt.Println("Superset metadata database — paste this when `fedctl install` asks for one:")
	fmt.Println("  postgresql://test:test@host.docker.internal:5433/superset_test")
	fmt.Println()
	fmt.Println("Before adding either source below, allow this host once in .env:")
	fmt.Println("  FEDCTL_ALLOWED_SOURCE_HOSTS=host.docker.internal")
	fmt.Println()
	fmt.Println("Federated PostgreSQL source (password: sample):")
	fmt.Println("  fedctl source add --name testpg --type postgres \\")
	fmt.Println("      --host host.docker.internal --port 5434 --user sample --database sampledb")
	fmt.Println()
	fmt.Println("Federated MySQL source (password: sample):")
	fmt.Println("  fedctl source add --name testmy --type mysql \\")
	fmt.Println("      --host host.docker.internal --port 3307 --user sample --database sampledb")
	fmt.Println()
	fmt.Println("Federated SQLite source — upload test/testkit/region_notes.sqlite through the")
	fmt.Println("panel's Add Source picker, or from the CLI:")
	fmt.Println("  fedctl seed sqlite --from test/testkit/region_notes.sqlite")
	fmt.Println("  fedctl source add --name regionnotes --type sqlite --path region_notes.sqlite")
	fmt.Println()
	fmt.Println("Demo join, once all three are attached:")
	fmt.Println("  SELECT c.name, o.amount_cents, r.note")
	fmt.Println("  FROM fed_testpg.customers c")
	fmt.Println("  JOIN fed_testmy.orders o ON o.customer_id = c.id")
	fmt.Println("  JOIN fed_regionnotes.region_notes r ON r.region = c.region")
	fmt.Println()
	fmt.Println("Reprint this any time:  fedctl testkit info")
	fmt.Println("Tear everything down:   fedctl testkit down")
	fmt.Println()
	fmt.Println("Using plain Linux Docker Engine (no Docker Desktop) and the metadata database")
	fmt.Println("above isn't reachable? Add this line to /etc/hosts once:")
	fmt.Println("  127.0.0.1 host.docker.internal")
	fmt.Println()
}
