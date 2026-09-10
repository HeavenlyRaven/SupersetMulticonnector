package ddl

import (
	"strings"
	"testing"
)

const secretPassword = "sup3r-secret-hunter2"

func assertNoSecretLeak(t *testing.T, plan Plan) {
	t.Helper()
	for _, step := range plan.Steps {
		if strings.Contains(step.Do.Text, secretPassword) {
			t.Errorf("statement text leaks the password: %q", step.Do.Text)
		}
		if step.Undo != nil && strings.Contains(step.Undo.Text, secretPassword) {
			t.Errorf("undo statement text leaks the password: %q", step.Undo.Text)
		}
		if strings.Contains(step.Do.Text, "?") == false && len(step.Do.Args) > 0 {
			t.Errorf("statement has bind args but no placeholder: %q", step.Do.Text)
		}
	}
}

func TestAttachPostgres_Golden(t *testing.T) {
	plan, err := AttachPostgres("fed_sales", map[string]string{
		"host": "pg.internal", "port": "5432", "user": "svc", "password": secretPassword,
		"database": "salesdb", "schema": "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecretLeak(t, plan)

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	wantColl := "CREATE NAMED COLLECTION IF NOT EXISTS `fed_fed_sales_creds` AS host = ?, port = ?, user = ?, password = ?, database = ?, schema = ?"
	if plan.Steps[0].Do.Text != wantColl {
		t.Errorf("collection DDL mismatch:\n got: %s\nwant: %s", plan.Steps[0].Do.Text, wantColl)
	}
	wantArgs := []any{"pg.internal", 5432, "svc", secretPassword, "salesdb", "public"}
	gotArgs := plan.Steps[0].Do.Values()
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("arg count mismatch: got %v", gotArgs)
	}
	for i, a := range wantArgs {
		if gotArgs[i] != a {
			t.Errorf("arg[%d]: got %v want %v", i, gotArgs[i], a)
		}
	}
	if len(plan.Steps[0].Do.Secrets()) != 1 || plan.Steps[0].Do.Secrets()[0] != secretPassword {
		t.Errorf("expected exactly the password marked secret, got %v", plan.Steps[0].Do.Secrets())
	}
	if len(plan.Steps[1].Do.Secrets()) != 0 {
		t.Errorf("database-create statement should have no secret args, got %v", plan.Steps[1].Do.Secrets())
	}

	wantDB := "CREATE DATABASE IF NOT EXISTS `fed_fed_sales` ENGINE = PostgreSQL(`fed_fed_sales_creds`) COMMENT ?"
	if plan.Steps[1].Do.Text != wantDB {
		t.Errorf("database DDL mismatch:\n got: %s\nwant: %s", plan.Steps[1].Do.Text, wantDB)
	}
	if got := plan.Steps[1].Do.Values(); len(got) != 1 || got[0] != "host=pg.internal" {
		t.Errorf("expected database comment to embed the host, got %v", got)
	}

	if plan.Steps[0].Undo == nil || plan.Steps[0].Undo.Text != "DROP NAMED COLLECTION IF EXISTS `fed_fed_sales_creds`" {
		t.Errorf("unexpected undo for step 0: %+v", plan.Steps[0].Undo)
	}
	if plan.Steps[1].Undo == nil || plan.Steps[1].Undo.Text != "DROP DATABASE IF EXISTS `fed_fed_sales`" {
		t.Errorf("unexpected undo for step 1: %+v", plan.Steps[1].Undo)
	}
}

func TestAttachPostgres_DefaultSchema(t *testing.T) {
	plan, err := AttachPostgres("src1", map[string]string{
		"host": "h", "port": "5432", "user": "u", "password": "p", "database": "d",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].Do.Values()[5] != "public" {
		t.Errorf("expected default schema 'public', got %v", plan.Steps[0].Do.Values()[5])
	}
}

func TestAttachPostgres_RejectsBadName(t *testing.T) {
	if _, err := AttachPostgres("foo; DROP DATABASE default", map[string]string{
		"host": "h", "port": "5432", "user": "u", "password": "p", "database": "d",
	}); err == nil {
		t.Fatal("expected rejection of malicious source name")
	}
}

func TestAttachPostgres_MissingRequiredField(t *testing.T) {
	if _, err := AttachPostgres("src1", map[string]string{"host": "h", "port": "5432"}); err == nil {
		t.Fatal("expected rejection of missing required fields")
	}
}

func TestAttachPostgres_BadPort(t *testing.T) {
	if _, err := AttachPostgres("src1", map[string]string{
		"host": "h", "port": "not-a-number", "user": "u", "password": "p", "database": "d",
	}); err == nil {
		t.Fatal("expected rejection of non-numeric port")
	}
	if _, err := AttachPostgres("src1", map[string]string{
		"host": "h", "port": "99999", "user": "u", "password": "p", "database": "d",
	}); err == nil {
		t.Fatal("expected rejection of out-of-range port")
	}
}

func TestAttachMySQL_Golden(t *testing.T) {
	plan, err := AttachMySQL("fed_orders", map[string]string{
		"host": "mysql.internal", "port": "3306", "user": "svc", "password": secretPassword, "database": "orders",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecretLeak(t, plan)

	wantColl := "CREATE NAMED COLLECTION IF NOT EXISTS `fed_fed_orders_creds` AS host = ?, port = ?, user = ?, password = ?, database = ?"
	if plan.Steps[0].Do.Text != wantColl {
		t.Errorf("collection DDL mismatch:\n got: %s\nwant: %s", plan.Steps[0].Do.Text, wantColl)
	}
	wantDB := "CREATE DATABASE IF NOT EXISTS `fed_fed_orders` ENGINE = MySQL(`fed_fed_orders_creds`) COMMENT ?"
	if plan.Steps[1].Do.Text != wantDB {
		t.Errorf("database DDL mismatch:\n got: %s\nwant: %s", plan.Steps[1].Do.Text, wantDB)
	}
	if got := plan.Steps[1].Do.Values(); len(got) != 1 || got[0] != "host=mysql.internal" {
		t.Errorf("expected database comment to embed the host, got %v", got)
	}
}

func TestAttachSQLite_Golden(t *testing.T) {
	plan, err := AttachSQLite("ref_data", "/var/lib/clickhouse/user_files/app.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("sqlite must have exactly one step (no named collection), got %d", len(plan.Steps))
	}
	wantDB := "CREATE DATABASE IF NOT EXISTS `fed_ref_data` ENGINE = SQLite(?)"
	if plan.Steps[0].Do.Text != wantDB {
		t.Errorf("database DDL mismatch:\n got: %s\nwant: %s", plan.Steps[0].Do.Text, wantDB)
	}
	if plan.Steps[0].Do.Values()[0] != "/var/lib/clickhouse/user_files/app.sqlite" {
		t.Errorf("unexpected arg: %v", plan.Steps[0].Do.Values())
	}
	if len(plan.Steps[0].Do.Secrets()) != 0 {
		t.Errorf("sqlite path is not a secret, got %v", plan.Steps[0].Do.Secrets())
	}
}

func TestDetach_WithCollection(t *testing.T) {
	plan, err := Detach("fed_sales", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Do.Text != "DROP DATABASE IF EXISTS `fed_fed_sales`" {
		t.Errorf("unexpected step 0: %s", plan.Steps[0].Do.Text)
	}
	if plan.Steps[1].Do.Text != "DROP NAMED COLLECTION IF EXISTS `fed_fed_sales_creds`" {
		t.Errorf("unexpected step 1: %s", plan.Steps[1].Do.Text)
	}
}

func TestDetach_WithoutCollection(t *testing.T) {
	plan, err := Detach("ref_data", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step for sqlite (no collection), got %d", len(plan.Steps))
	}
}

func TestPlan_StatementsForPlanOutput(t *testing.T) {
	plan, _ := AttachPostgres("src1", map[string]string{
		"host": "h", "port": "5432", "user": "u", "password": secretPassword, "database": "d",
	})
	stmts := plan.Statements()
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	for _, s := range stmts {
		if strings.Contains(s.Text, secretPassword) {
			t.Errorf("`fedctl plan` output must never contain the password: %s", s.Text)
		}
	}
}
