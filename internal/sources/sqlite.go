package sources

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"

	"github.com/acme/superset-federation/internal/jsonio"
	"github.com/acme/superset-federation/internal/validate"
)

func init() { Register(sqliteType{}) }

type sqliteType struct{}

func (sqliteType) Name() string         { return "sqlite" }
func (sqliteType) Engine() string       { return "SQLite" }
func (sqliteType) HasCredentials() bool { return false }

func (sqliteType) Fields() []Field {
	return []Field{
		{Name: "path", Label: "File", Type: "path", Required: true,
			HelperText: "pick a file already uploaded, or upload a new one"},
	}
}

// Validate resolves and validates params["path"] against userFilesDir via
// validate.SQLitePath, rejecting traversal and escaping symlinks. The
// returned Params["path"] is the resolved absolute container path, ready
// to pass straight into ddl.AttachSQLite.
func (sqliteType) Validate(userFilesDir string, params Params) (Params, error) {
	if strings.TrimSpace(params["path"]) == "" {
		return nil, jsonio.NewError(jsonio.CodeInvalidPath, "sqlite source requires \"path\"", "")
	}
	resolved, err := validate.SQLitePath(userFilesDir, params["path"])
	if err != nil {
		return nil, err
	}
	return Params{"path": resolved}, nil
}

// TestConnection opens the (already-resolved, by Validate) sqlite file
// directly with modernc.org/sqlite — the pure-Go driver from spec
// section 4's compatibility table — independent of ClickHouse.
//
// It checks the file exists first: SQLite drivers (this one included)
// silently create an empty database on open if the path doesn't exist,
// which would otherwise make "test connection" against a mistyped path
// report a false success with zero tables instead of a clear error.
func (sqliteType) TestConnection(ctx context.Context, _ string, p Params) (int, error) {
	if _, err := os.Stat(p["path"]); err != nil {
		return 0, jsonio.NewError(jsonio.CodeInvalidPath, fmt.Sprintf("sqlite file %q does not exist", p["path"]), "")
	}
	db, err := sql.Open("sqlite", p["path"])
	if err != nil {
		return 0, jsonio.NewError(jsonio.CodeInternal, "opening sqlite file: "+err.Error(), "")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var count int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&count)
	if err != nil {
		return 0, jsonio.NewError(jsonio.CodeConnectionFailed, "reading sqlite file failed: "+err.Error(), "")
	}
	return count, nil
}
