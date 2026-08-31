package hub

import (
	"context"
	"database/sql"

	"github.com/acme/superset-federation/internal/jsonio"
)

// DatabaseEngine returns the ENGINE of a database on the hub, or
// jsonio.CodeSourceNotFound if it doesn't exist.
func (c *Conn) DatabaseEngine(ctx context.Context, name string) (string, error) {
	var engine string
	err := c.db.QueryRowContext(ctx, "SELECT engine FROM system.databases WHERE name = ?", name).Scan(&engine)
	if err == sql.ErrNoRows {
		return "", jsonio.NewError(jsonio.CodeSourceNotFound, "database "+name+" does not exist", "")
	}
	if err != nil {
		return "", jsonio.NewError(jsonio.CodeInternal, "looking up database engine: "+err.Error(), "")
	}
	return engine, nil
}
