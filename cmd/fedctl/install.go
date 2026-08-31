package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/acme/superset-federation/internal/config"
	"github.com/acme/superset-federation/internal/preflight"
)

// newInstallCmd is the one command a brand-new user needs: it checks for
// Docker, interactively writes .env (auto-generating every internal
// secret — you only answer for things fedctl can't know on its own, like
// the external Postgres DSN), runs preflight, and brings the stack up.
// Distributing fedctl (see the Makefile's cross-build target, or a
// release archive containing this binary plus the repo checkout) IS
// distributing the installer — there is no separate script.
func newInstallCmd() *cobra.Command {
	var yes, force, skipUp, dev bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "First-time setup: checks Docker, writes .env, runs preflight, and brings the stack up",
		Long: "Run this once from inside a checkout of this repository.\n\n" +
			"It checks for Docker with Compose v2 and buildx, interactively writes .env\n" +
			"(every internal secret — the ClickHouse role passwords, Superset's Flask\n" +
			"secret key — is auto-generated; you're only asked for things fedctl can't\n" +
			"know on its own, like the external PostgreSQL URI), runs the same checks as\n" +
			"`fedctl preflight`, and then the same thing `fedctl up` does.\n\n" +
			"Safe to re-run: an existing .env is left alone unless --force is given, and\n" +
			"every step downstream of it (preflight, up) is already idempotent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runInstall(cmd.Context(), installOptions{Yes: yes, Force: force, SkipUp: skipUp, Dev: dev}); err != nil {
				printCLIError(err)
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "non-interactive: accept defaults and auto-generate secrets without prompting (reads required values, e.g. SQLALCHEMY_DATABASE_URI, from the environment)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .env instead of leaving it alone")
	cmd.Flags().BoolVar(&skipUp, "skip-up", false, "stop after writing .env and running preflight; don't start the stack")
	cmd.Flags().BoolVar(&dev, "dev", false, "also start compose.dev.yaml's sample source databases when bringing the stack up")
	return cmd
}

type installOptions struct {
	Yes, Force, SkipUp, Dev bool
}

func runInstall(ctx context.Context, opts installOptions) error {
	fmt.Println("=== Superset + ClickHouse federation hub: first-time setup ===")
	fmt.Println()

	if err := checkRepoCheckout(); err != nil {
		return err
	}

	if err := dockerAvailable(ctx); err != nil {
		printDockerInstallHelp()
		return err
	}
	fmt.Println("[ok] docker found on PATH")

	if fileExists(".env") && !opts.Force {
		fmt.Println("[ok] .env already exists — leaving it alone (use --force to regenerate)")
	} else {
		if err := writeEnvFile(opts.Yes); err != nil {
			return err
		}
		fmt.Println("[ok] wrote .env")
	}

	// main() already called loadDotEnv(".env") once, before .env existed
	// (a first run) or before this process's own env had a chance to
	// reflect a just-written file (--force). Call it again now that the
	// file is known-current, so preflight/up below — and everything
	// config.LoadApp() reads from os.Getenv — see it within this same
	// process, not just in the containers Compose starts later.
	loadDotEnv(".env")

	fmt.Println()
	fmt.Println("running preflight checks...")
	app, err := config.LoadApp()
	if err != nil {
		return err
	}
	results, ok := preflight.Run(ctx, preflight.Options{
		PinnedImages: pinnedImages(),
		MetadataURI:  app.SupersetMetadataURI,
	})
	printPreflightResults(results)
	if !ok {
		return fmt.Errorf("preflight failed — fix the issues above, then re-run `fedctl install`")
	}

	if opts.SkipUp {
		fmt.Println()
		fmt.Println("Setup complete. Run `fedctl up` when you're ready to start the stack.")
		return nil
	}

	if !opts.Yes && !confirm("Bring the stack up now? [Y/n] ", true) {
		fmt.Println("Setup complete. Run `fedctl up` when you're ready.")
		return nil
	}

	fmt.Println()
	fmt.Println("bringing the stack up — this builds both images, so the first run can take a few minutes...")
	if err := runUp(ctx, opts.Dev, 5*time.Minute); err != nil {
		return err
	}

	port := os.Getenv("SUPERSET_PORT")
	if port == "" {
		port = "8088"
	}
	fmt.Println()
	fmt.Printf("Done. Superset is running at http://localhost:%s\n", port)
	return nil
}

func checkRepoCheckout() error {
	if _, err := os.Stat("compose.yaml"); err != nil {
		return fmt.Errorf("compose.yaml not found in the current directory — run `fedctl install` from inside a checkout of this repository")
	}
	if _, err := os.Stat(".env.example"); err != nil {
		return fmt.Errorf(".env.example not found in the current directory — run `fedctl install` from inside a checkout of this repository")
	}
	return nil
}

func printDockerInstallHelp() {
	fmt.Println()
	fmt.Println("Docker was not found on PATH. Install it first, then re-run `fedctl install`:")
	switch runtime.GOOS {
	case "darwin":
		fmt.Println("  macOS:   https://docs.docker.com/desktop/install/mac-install/")
	case "windows":
		fmt.Println("  Windows: https://docs.docker.com/desktop/install/windows-install/ (uses the WSL2 backend)")
	default:
		fmt.Println("  Linux:   https://docs.docker.com/engine/install/")
		fmt.Println("           also install the docker-compose-plugin and docker-buildx-plugin packages")
	}
	fmt.Println()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// envField describes how to resolve one .env.example key. Exactly one of
// Generate/Secret/Prompt-with-Validate/Prompt/Default applies per field —
// see resolveEnvValues.
type envField struct {
	Key      string
	Prompt   string
	Default  string
	Generate bool // always auto-generate a random value; never prompted
	Secret   bool // prompt with masked input; blank answer auto-generates
	Validate func(string) error
}

// envFields mirrors .env.example — keep the two in sync. Anything not
// listed here is left exactly as .env.example has it.
var envFields = []envField{
	{Key: "SQLALCHEMY_DATABASE_URI", Prompt: "External PostgreSQL URI for Superset's metadata database (postgresql://user:pass@host:5432/db)", Validate: config.ValidateMetadataURI},
	{Key: "SUPERSET_SECRET_KEY", Generate: true},
	{Key: "SUPERSET_ADMIN_USER", Prompt: "Superset admin username", Default: "admin"},
	{Key: "SUPERSET_ADMIN_PASSWORD", Prompt: "Superset admin password (leave blank to auto-generate)", Secret: true},
	{Key: "FEDCTL_ADMIN_USER", Default: "fed_admin"},
	{Key: "FEDCTL_ADMIN_PASSWORD", Generate: true},
	{Key: "SUPERSET_CH_USER", Default: "bi_ro"},
	{Key: "SUPERSET_CH_PASSWORD", Generate: true},
	{Key: "CLICKHOUSE_MAX_BYTES_IN_JOIN", Default: "2000000000"},
	{Key: "CLICKHOUSE_MAX_MEMORY_USAGE", Default: "4000000000"},
	{Key: "CLICKHOUSE_MEMORY_LIMIT", Default: "4g"},
	{Key: "SUPERSET_MEMORY_LIMIT", Default: "4g"},
	{Key: "SQLLAB_TIMEOUT", Default: "300"},
	{Key: "SUPERSET_WEBSERVER_TIMEOUT", Default: "300"},
	{Key: "WEB_CONCURRENCY", Default: "8"},
	{Key: "SUPERSET_PORT", Prompt: "Port to publish Superset on", Default: "8088"},
	{Key: "FEDCTL_ALLOWED_SOURCE_HOSTS", Default: ""},
	{Key: "LOCAL_EXTENSIONS", Default: ""},
}

var envKeyLineRe = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=`)

// writeEnvFile resolves every envFields entry and writes .env by
// rewriting .env.example line by line — every comment and every key this
// repo might add later that isn't in envFields passes through unchanged,
// so .env.example stays the single source of truth for documentation.
func writeEnvFile(nonInteractive bool) error {
	exampleBytes, err := os.ReadFile(".env.example")
	if err != nil {
		return fmt.Errorf("reading .env.example: %w", err)
	}

	if !nonInteractive {
		fmt.Println()
		fmt.Println("Setting up .env — press Enter to accept a shown default.")
	}
	values, revealedAdminPassword, err := resolveEnvValues(nonInteractive)
	if err != nil {
		return err
	}

	lines := strings.Split(string(exampleBytes), "\n")
	for i, line := range lines {
		m := envKeyLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if v, ok := values[m[1]]; ok {
			lines[i] = m[1] + "=" + v
		}
	}

	// 0600: this file holds every credential the stack uses.
	if err := os.WriteFile(".env", []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}

	if revealedAdminPassword != "" {
		fmt.Println()
		fmt.Println("IMPORTANT — generated Superset admin password (shown once, also saved in .env):")
		fmt.Println("  " + revealedAdminPassword)
	}
	return nil
}

func resolveEnvValues(nonInteractive bool) (values map[string]string, revealedAdminPassword string, err error) {
	values = map[string]string{}

	for _, f := range envFields {
		switch {
		case f.Generate:
			secret, genErr := generateSecret(24)
			if genErr != nil {
				return nil, "", genErr
			}
			values[f.Key] = secret

		case f.Secret:
			var v string
			if nonInteractive {
				v = os.Getenv(f.Key)
			} else {
				fmt.Println()
				v = promptSecret(f.Prompt)
			}
			if v == "" {
				secret, genErr := generateSecret(12)
				if genErr != nil {
					return nil, "", genErr
				}
				v = secret
				revealedAdminPassword = secret
			}
			values[f.Key] = v

		case f.Validate != nil:
			v, verr := resolvePromptedRequired(f, nonInteractive)
			if verr != nil {
				return nil, "", verr
			}
			values[f.Key] = v

		case f.Prompt != "":
			var v string
			if nonInteractive {
				v = os.Getenv(f.Key)
				if v == "" {
					v = f.Default
				}
			} else {
				fmt.Println()
				v = promptLine(f.Prompt, f.Default)
			}
			values[f.Key] = v

		default:
			values[f.Key] = f.Default
		}
	}
	return values, revealedAdminPassword, nil
}

func resolvePromptedRequired(f envField, nonInteractive bool) (string, error) {
	if nonInteractive {
		v := os.Getenv(f.Key)
		if v == "" {
			return "", fmt.Errorf("%s must be set in the environment when using --yes (it has no safe default)", f.Key)
		}
		if err := f.Validate(v); err != nil {
			return "", fmt.Errorf("%s: %w", f.Key, err)
		}
		return v, nil
	}
	fmt.Println()
	for {
		v := promptLine(f.Prompt, f.Default)
		if v == "" {
			fmt.Println("  this value is required")
			continue
		}
		if err := f.Validate(v); err != nil {
			fmt.Println("  " + err.Error())
			continue
		}
		return v, nil
	}
}

func generateSecret(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating a random secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// readLine reads one line directly from os.Stdin, one byte at a time,
// deliberately without a buffered reader: term.ReadPassword (used by
// promptSecret) also reads raw from the same fd, and a bufio.Reader's
// read-ahead would silently swallow bytes term.ReadPassword needed —
// this keeps the two compatible with each other.
func readLine() string {
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			if b[0] != '\r' {
				buf = append(buf, b[0])
			}
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(string(buf))
}

func promptLine(prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	if v := readLine(); v != "" {
		return v
	}
	return def
}

func promptSecret(prompt string) string {
	fmt.Printf("%s: ", prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return readLine()
}

func confirm(prompt string, def bool) bool {
	fmt.Print(prompt)
	v := strings.ToLower(readLine())
	if v == "" {
		return def
	}
	return v == "y" || v == "yes"
}
