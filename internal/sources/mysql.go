package sources

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

func init() { Register(mysqlType{}) }

type mysqlType struct{}

func (mysqlType) Name() string         { return "mysql" }
func (mysqlType) Engine() string       { return "MySQL" }
func (mysqlType) HasCredentials() bool { return true }

func (mysqlType) Fields() []Field {
	return []Field{
		{Name: "host", Label: "Host", Type: "text", Required: true},
		{Name: "port", Label: "Port", Type: "number", Required: true, Default: "3306"},
		{Name: "user", Label: "User", Type: "text", Required: true},
		{Name: "password", Label: "Password", Type: "password", Required: true},
		{Name: "database", Label: "Database", Type: "text", Required: true},
	}
}

func (mysqlType) Validate(_ string, params Params) (Params, error) {
	out := Params{}
	for k, v := range params {
		out[k] = v
	}
	for _, req := range []string{"host", "port", "user", "password", "database"} {
		if strings.TrimSpace(out[req]) == "" {
			return nil, jsonio.NewError(jsonio.CodeInvalidRequest,
				fmt.Sprintf("mysql source requires %q", req), "")
		}
	}
	if err := validate.Port(atoiOrZero(out["port"])); err != nil {
		return nil, err
	}
	return out, nil
}

// TestConnection dials MySQL directly with go-sql-driver/mysql — the
// pure-Go driver from spec section 4's compatibility table — independent
// of ClickHouse, so "Test connection" is fast and leaves no
// ClickHouse-side state.
func (mysqlType) TestConnection(ctx context.Context, _ string, p Params) (int, error) {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", p["host"], atoiOrZero(p["port"]))
	cfg.User = p["user"]
	cfg.Passwd = p["password"]
	cfg.DBName = p["database"]
	cfg.Timeout = 10 * time.Second

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return 0, jsonio.NewError(jsonio.CodeInternal, "building mysql connection: "+err.Error(), "")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return 0, jsonio.NewError(jsonio.CodeConnectionFailed, "cannot connect to mysql source: "+err.Error(), "")
	}
	var count int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema = ?", p["database"]).Scan(&count)
	if err != nil {
		return 0, jsonio.NewError(jsonio.CodeConnectionFailed, "connected, but listing tables failed: "+err.Error(), "")
	}
	return count, nil
}
