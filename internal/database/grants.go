// Package database renders generated-resource MariaDB statements without executing SQL.
package database

import (
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"strings"
)

// Grants contains only nonsecret statements. Account creation and password handling
// belong to the separately gated execution layer, not this renderer.
type Grants struct {
	CreateDatabase string
	DropDatabase   string
	Grant          string
}

// RenderGrants accepts only d_<lowercase ULID> and u_<lowercase ULID>.
// The account host and database-scoped privilege allowlist are fixed.
func RenderGrants(databaseName, username string) (Grants, error) {
	if !generatedName(databaseName, "d_") || !generatedName(username, "u_") {
		return Grants{}, rpcerr.Errf("invalid_argument", "invalid generated database or account name")
	}
	quoted := quoteIdentifier(databaseName)
	return Grants{
		CreateDatabase: "CREATE DATABASE " + quoted + " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;",
		DropDatabase:   "DROP DATABASE " + quoted + ";",
		Grant:          "GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES ON " + quoteIdentifier(escapeGrantPattern(databaseName)) + ".* TO '" + username + "'@'127.0.0.1';",
	}, nil
}

func generatedName(name, prefix string) bool {
	if len(name) != 28 || !strings.HasPrefix(name, prefix) || name != strings.ToLower(name) {
		return false
	}
	return id.Valid(strings.ToUpper(name[2:])) && (&validate.V{}).DBName(name) == nil
}
func quoteIdentifier(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }
func escapeGrantPattern(name string) string {
	return strings.NewReplacer("\\", "\\\\", "_", "\\_", "%", "\\%").Replace(name)
}
