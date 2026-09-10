package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/sources"
)

func newSourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "source",
		Short: "Manage federated sources on the ClickHouse hub",
	}
	cmd.AddCommand(newSourceTypesCmd())
	cmd.AddCommand(newSourceListCmd())
	cmd.AddCommand(newSourceAddCmd())
	cmd.AddCommand(newSourceRemoveCmd())
	cmd.AddCommand(newSourceTestCmd())
	cmd.AddCommand(newSourceTablesCmd())
	cmd.AddCommand(newSourceProbeCmd())
	cmd.AddCommand(newSQLiteFilesCmd())
	return cmd
}

// readSecret reads one line from stdin and trims its trailing newline —
// used for the password prompt in plain (non---json) CLI mode, so a
// credential is never accepted as a flag. In --json mode the whole
// request, password included, arrives as one JSON object instead.
func readSecret(prompt string) (string, error) {
	if term := os.Getenv("FEDCTL_NO_PROMPT"); term == "" {
		fmt.Fprint(os.Stderr, prompt)
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", jsonio.NewError(jsonio.CodeInvalidRequest, "could not read password from stdin: "+err.Error(), "")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func newSourceTypesCmd() *cobra.Command {
	var jsonMode bool
	cmd := &cobra.Command{
		Use:   "types",
		Short: "List registered source types and their form fields",
		RunE: func(cmd *cobra.Command, args []string) error {
			result := doSourceTypes()
			if jsonMode {
				return writeJSONResult(result, nil)
			}
			return printHuman(result)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "write a jsonio envelope to stdout")
	return cmd
}

func newSourceListCmd() *cobra.Command {
	var jsonMode bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List federated sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}
			conn, err := openHub(ctx, app)
			if err != nil {
				return writeOrPrintError(jsonMode, err)
			}
			defer conn.Close()
			result, err := doListSources(ctx, conn)
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "read/write via the jsonio envelope")
	return cmd
}

func newSourceAddCmd() *cobra.Command {
	var jsonMode bool
	var name, srcType, host, port, user, database, schema, path string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Attach a new federated source",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}

			var req addSourceRequest
			if jsonMode {
				if err := readJSONRequest(&req); err != nil {
					return writeJSONResult(nil, err)
				}
			} else {
				if name == "" || srcType == "" {
					err := jsonio.NewError(jsonio.CodeInvalidRequest, "--name and --type are required", "")
					return writeOrPrintError(false, err)
				}
				params := map[string]string{
					"host": host, "port": port, "user": user,
					"database": database, "schema": schema, "path": path,
				}
				if ty, ok := sources.Get(srcType); ok && ty.HasCredentials() {
					pw, err := readSecret(fmt.Sprintf("password for %s %q: ", srcType, name))
					if err != nil {
						return writeOrPrintError(false, err)
					}
					params["password"] = pw
				}
				req = addSourceRequest{Name: name, Type: srcType, Params: params}
			}

			conn, err := openHub(ctx, app)
			if err != nil {
				return writeOrPrintError(jsonMode, err)
			}
			defer conn.Close()

			result, err := doAddSource(ctx, conn, app, req)
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "read request from stdin, write envelope to stdout")
	cmd.Flags().StringVar(&name, "name", "", "source name")
	cmd.Flags().StringVar(&srcType, "type", "", "source type: postgres, mysql, sqlite")
	cmd.Flags().StringVar(&host, "host", "", "source host")
	cmd.Flags().StringVar(&port, "port", "", "source port")
	cmd.Flags().StringVar(&user, "user", "", "source user")
	cmd.Flags().StringVar(&database, "database", "", "source database")
	cmd.Flags().StringVar(&schema, "schema", "", "source schema (postgres only)")
	cmd.Flags().StringVar(&path, "path", "", "sqlite file path, relative to the user_files volume")
	return cmd
}

func newSourceRemoveCmd() *cobra.Command {
	var jsonMode bool
	var name string
	cmd := &cobra.Command{
		Use:   "remove [name]",
		Short: "Detach a federated source",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}
			if jsonMode {
				var req struct {
					Name string `json:"name"`
				}
				if err := readJSONRequest(&req); err != nil {
					return writeJSONResult(nil, err)
				}
				name = req.Name
			} else if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				err := jsonio.NewError(jsonio.CodeInvalidRequest, "source name is required", "")
				return writeOrPrintError(jsonMode, err)
			}
			conn, err := openHub(ctx, app)
			if err != nil {
				return writeOrPrintError(jsonMode, err)
			}
			defer conn.Close()
			result, err := doRemoveSource(ctx, conn, name)
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "read request from stdin, write envelope to stdout")
	return cmd
}

func newSourceTestCmd() *cobra.Command {
	var jsonMode bool
	var name, srcType, host, port, user, database, schema, path string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test connectivity for a source's parameters without persisting it",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}
			var req testSourceRequest
			if jsonMode {
				if err := readJSONRequest(&req); err != nil {
					return writeJSONResult(nil, err)
				}
			} else {
				if name == "" || srcType == "" {
					err := jsonio.NewError(jsonio.CodeInvalidRequest, "--name and --type are required", "")
					return writeOrPrintError(false, err)
				}
				params := map[string]string{
					"host": host, "port": port, "user": user,
					"database": database, "schema": schema, "path": path,
				}
				if ty, ok := sources.Get(srcType); ok && ty.HasCredentials() {
					pw, err := readSecret(fmt.Sprintf("password for %s %q: ", srcType, name))
					if err != nil {
						return writeOrPrintError(false, err)
					}
					params["password"] = pw
				}
				req = testSourceRequest{Name: name, Type: srcType, Params: params}
			}
			// No hub connection here on purpose: doTestSource dials the
			// source directly and never touches ClickHouse.
			result, err := doTestSource(ctx, app, req)
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "read request from stdin, write envelope to stdout")
	cmd.Flags().StringVar(&name, "name", "", "source name")
	cmd.Flags().StringVar(&srcType, "type", "", "source type: postgres, mysql, sqlite")
	cmd.Flags().StringVar(&host, "host", "", "source host")
	cmd.Flags().StringVar(&port, "port", "", "source port")
	cmd.Flags().StringVar(&user, "user", "", "source user")
	cmd.Flags().StringVar(&database, "database", "", "source database")
	cmd.Flags().StringVar(&schema, "schema", "", "source schema (postgres only)")
	cmd.Flags().StringVar(&path, "path", "", "sqlite file path, relative to the user_files volume")
	return cmd
}

func newSourceTablesCmd() *cobra.Command {
	var jsonMode bool
	var name string
	cmd := &cobra.Command{
		Use:   "tables [name]",
		Short: "List tables ClickHouse currently sees in a federated source",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}
			if jsonMode {
				var req struct {
					Name string `json:"name"`
				}
				if err := readJSONRequest(&req); err != nil {
					return writeJSONResult(nil, err)
				}
				name = req.Name
			} else if len(args) == 1 {
				name = args[0]
			}
			conn, err := openHub(ctx, app)
			if err != nil {
				return writeOrPrintError(jsonMode, err)
			}
			defer conn.Close()
			result, err := doTables(ctx, conn, name)
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "read request from stdin, write envelope to stdout")
	return cmd
}

func newSourceProbeCmd() *cobra.Command {
	var jsonMode bool
	var name string
	cmd := &cobra.Command{
		Use:   "probe [name]",
		Short: "Run a live connectivity check against an existing source",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(jsonMode)
			if err != nil {
				return err
			}
			if jsonMode {
				var req struct {
					Name string `json:"name"`
				}
				if err := readJSONRequest(&req); err != nil {
					return writeJSONResult(nil, err)
				}
				name = req.Name
			} else if len(args) == 1 {
				name = args[0]
			}
			conn, err := openHub(ctx, app)
			if err != nil {
				return writeOrPrintError(jsonMode, err)
			}
			defer conn.Close()
			reader, err := openBIReadOnly(ctx, app)
			if err != nil {
				return writeOrPrintError(jsonMode, err)
			}
			defer reader.Close()
			result, err := doProbe(ctx, conn, reader, name)
			return finish(jsonMode, result, err)
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "read request from stdin, write envelope to stdout")
	return cmd
}

// finish is the common tail of every source subcommand's RunE: in --json
// mode it always writes exactly one envelope to stdout; in plain mode it
// prints the error to stderr or the result as indented JSON.
func finish(jsonMode bool, result any, err error) error {
	if jsonMode {
		return writeJSONResult(result, err)
	}
	if err != nil {
		printCLIError(err)
		return err
	}
	return printHuman(result)
}

func writeOrPrintError(jsonMode bool, err error) error {
	if jsonMode {
		_ = jsonio.WriteErr(os.Stdout, err)
		return err
	}
	printCLIError(err)
	return err
}
