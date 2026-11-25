// Package db holds the SQL schema files and applies the Fraud Shield migration.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed migrations/003_risk.sql
var riskMigration string

//go:embed schema_enhanced.sql
var schemaSQL string

//go:embed procedures.sql
var proceduresSQL string

//go:embed views.sql
var viewsSQL string

// InitializeSchema creates all tables, procedures and views if they do not exist.
func InitializeSchema(ctx context.Context, conn *sql.DB) error {
	var exists bool
	err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'users')`).Scan(&exists)
	if err == nil && exists {
		return nil
	}

	if _, err := conn.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("execute schema_enhanced.sql: %w", err)
	}

	if _, err := conn.ExecContext(ctx, proceduresSQL); err != nil {
		return fmt.Errorf("execute procedures.sql: %w", err)
	}

	if _, err := conn.ExecContext(ctx, viewsSQL); err != nil {
		return fmt.Errorf("execute views.sql: %w", err)
	}

	return nil
}

// ApplyRiskMigration creates the Fraud Shield tables and statuses. It is idempotent.
func ApplyRiskMigration(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, riskMigration); err != nil {
		return fmt.Errorf("apply 003_risk.sql: %w", err)
	}
	return nil
}
