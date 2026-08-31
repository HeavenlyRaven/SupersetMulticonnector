package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/acme/superset-federation/internal/config"
	"github.com/acme/superset-federation/internal/ddl"
)

type applyResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Note string `json:"note,omitempty"`
}

func newApplyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Declaratively reconcile config/sources.yaml against the hub",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := loadApp(false)
			if err != nil {
				return err
			}
			sf, err := config.LoadSourcesFile(file)
			if err != nil {
				printCLIError(err)
				return err
			}
			conn, err := openHub(ctx, app)
			if err != nil {
				printCLIError(err)
				return err
			}
			defer conn.Close()

			var results []applyResult
			failed := false
			for _, spec := range sf.Sources {
				params, err := spec.ResolveParams()
				if err != nil {
					results = append(results, applyResult{Name: spec.Name, OK: false, Note: err.Error()})
					failed = true
					continue
				}
				// attachSource relies entirely on the plan's own IF NOT
				// EXISTS clauses for idempotence — this is the same
				// reconcile call `source add` makes, per spec section 9.
				if err := attachSource(ctx, conn, app, spec.Name, spec.Type, params); err != nil {
					results = append(results, applyResult{Name: spec.Name, OK: false, Note: err.Error()})
					failed = true
					continue
				}
				results = append(results, applyResult{Name: spec.Name, OK: true})
			}

			for _, r := range results {
				if r.OK {
					fmt.Printf("%s: ok\n", r.Name)
				} else {
					fmt.Fprintf(os.Stderr, "%s: FAILED: %s\n", r.Name, r.Note)
				}
			}
			if failed {
				return fmt.Errorf("one or more sources failed to apply")
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "config/sources.yaml", "path to the declarative sources file")
	return cmd
}

func newPlanCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Print the DDL that `apply` would run, credentials redacted",
		RunE: func(cmd *cobra.Command, args []string) error {
			sf, err := config.LoadSourcesFile(file)
			if err != nil {
				printCLIError(err)
				return err
			}
			for _, spec := range sf.Sources {
				fmt.Printf("-- source: %s (%s)\n", spec.Name, spec.Type)
				params, err := spec.ResolveParams()
				if err != nil {
					fmt.Printf("-- SKIPPED: %s\n\n", err.Error())
					continue
				}
				// Password is never resolved into DDL text — it is
				// always bound as a "?" placeholder — so printing the
				// plan is safe even though ResolveParams above did read
				// the real password from the environment for validation.
				plan, err := planFor(spec.Type, spec.Name, params)
				if err != nil {
					fmt.Printf("-- SKIPPED: %s\n\n", err.Error())
					continue
				}
				for _, stmt := range plan.Statements() {
					fmt.Println(stmt.Text + ";")
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "config/sources.yaml", "path to the declarative sources file")
	return cmd
}

func planFor(typeName, name string, params map[string]string) (ddl.Plan, error) {
	switch typeName {
	case "postgres":
		return ddl.AttachPostgres(name, params)
	case "mysql":
		return ddl.AttachMySQL(name, params)
	case "sqlite":
		return ddl.AttachSQLite(name, params["path"])
	default:
		return ddl.Plan{}, fmt.Errorf("unknown source type %q", typeName)
	}
}
