// Command fedctl is both the operator CLI for the Superset + ClickHouse
// federation stack and the backend the Python extension shim execs in
// --json mode. See internal/jsonio for the stdin/stdout contract shared
// with the shim.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

// version, commit, and date are set via -ldflags at build time (see
// images/superset/Dockerfile's Go build stage).
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	// Recover rather than let Go's default handler print the panic value
	// and a full goroutine stack to stderr: several command handlers
	// (doAddSource, doTestSource, attachSource) hold a source password in
	// a local variable, and a panic's message or a stack frame's
	// argument dump could echo it. Deliberately print nothing from the
	// panic itself — see internal/validate.Redactor's doc comment, which
	// names this exact path.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "fedctl: internal error (recovered from a panic) — this is a bug, please report it")
			os.Exit(2)
		}
	}()

	if err := guardCredentialFlags(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fedctl: "+err.Error())
		os.Exit(1)
	}

	// Load .env from the current directory before any command runs, so
	// `fedctl up`/`preflight`/`check`/... actually see what an operator
	// put in .env instead of only Docker Compose's containers seeing it.
	loadDotEnv(".env")

	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// guardCredentialFlags rejects any argv token that looks like a
// credential-shaped flag before Cobra even parses it. argv is
// world-readable via /proc on Linux, so a password passed as a flag is
// visible to every process on the host — see spec section 9.
func guardCredentialFlags(args []string) error {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		if validate.CredentialFlagName(name) {
			return fmt.Errorf("refusing a credential-shaped flag %q: credentials must be sent on stdin, never as a CLI argument (see `fedctl source add --help`)", a)
		}
	}
	return nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "fedctl",
		Short:         "Operate the Superset + ClickHouse federation hub",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInstallCmd())
	root.AddCommand(newPreflightCmd())
	root.AddCommand(newUpCmd())
	root.AddCommand(newDownCmd())
	root.AddCommand(newSeedCmd())
	root.AddCommand(newSourceCmd())
	root.AddCommand(newApplyCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newVersionCmd())
	return root
}

// printCLIError writes a human-readable error to stderr for plain
// (non---json) command failures. SilenceErrors is set on the root command
// so this is the only thing that writes CLI-mode errors — JSON-mode
// commands instead write the jsonio.Response envelope to stdout via
// writeJSONResult.
//
// This deliberately does NOT go through jsonio.AsError: that function's
// fallback to a generic "internal error" message exists to keep an
// unvetted raw error out of a --json API *response* (a different trust
// boundary — the caller may not be the operator). Here, the person
// running the command IS the operator looking at their own terminal, so
// they should see the real error — a docker/compose failure with a
// generic "internal error" instead of the actual message is useless for
// troubleshooting.
func printCLIError(err error) {
	var je *jsonio.Error
	if errors.As(err, &je) {
		msg := je.Info.Message
		if je.Info.Hint != "" {
			msg += " (" + je.Info.Hint + ")"
		}
		fmt.Fprintln(os.Stderr, "fedctl: "+msg)
		return
	}
	fmt.Fprintln(os.Stderr, "fedctl: "+err.Error())
}
